package automation

import (
	"encoding/json"
	"fmt"
	"time"

	"linkhub/internal/cluster"
	"linkhub/internal/models"
	"linkhub/internal/session"
	"linkhub/internal/store"
)

// Playbook 多步骤编排
type PlaybookStep struct {
	Targets  []map[string]interface{} `json:"targets"` // [{node_id, device_id}]
	Command  string                   `json:"command"`
	WaitMs   int                      `json:"wait_ms"`
	Parallel bool                     `json:"parallel"`
}

type Playbook struct {
	Name  string         `json:"name"`
	Steps []PlaybookStep `json:"steps"`
}

type StepResult struct {
	Step    int              `json:"step"`
	OK      bool             `json:"ok"`
	Results []map[string]any `json:"results"`
}

type Runner struct {
	store   *store.Store
	manager *session.Manager
	cluster *cluster.Cluster
}

func NewRunner(st *store.Store, mgr *session.Manager, cl *cluster.Cluster) *Runner {
	return &Runner{store: st, manager: mgr, cluster: cl}
}

func (r *Runner) Run(pb *Playbook) map[string]interface{} {
	steps := []StepResult{}
	for i, step := range pb.Steps {
		res := r.runStep(step, i)
		steps = append(steps, res)
		if !res.OK {
			break // 失败即停
		}
	}
	allOK := true
	for _, s := range steps {
		if !s.OK {
			allOK = false
			break
		}
	}
	return map[string]interface{}{"name": pb.Name, "ok": allOK, "steps": steps}
}

func (r *Runner) runStep(step PlaybookStep, idx int) StepResult {
	results := []map[string]interface{}{}
	groups := map[string][]int64{}
	for _, t := range step.Targets {
		nid, _ := t["node_id"].(string)
		did, _ := t["device_id"].(float64)
		if nid == "" || nid == "local" || nid == r.cluster.SelfID() {
			nid = "local"
		}
		groups[nid] = append(groups[nid], int64(did))
	}
	// 本机
	if ids, ok := groups["local"]; ok {
		results = append(results, r.execLocal(ids, step.Command, step.WaitMs)...)
	}
	// 远程节点
	for nid, ids := range groups {
		if nid == "local" {
			continue
		}
		results = append(results, r.execRemote(nid, ids, step.Command, step.WaitMs)...)
	}
	ok := true
	for _, r := range results {
		if !r["ok"].(bool) {
			ok = false
		}
	}
	return StepResult{Step: idx, OK: ok, Results: results}
}

func (r *Runner) execLocal(deviceIDs []int64, command string, waitMs int) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, did := range deviceIDs {
		dev, _ := r.store.GetDevice(did)
		name := fmt.Sprintf("#%d", did)
		if dev != nil {
			name = dev.Name
		}
		// 找第一个启用的连接
		conns, _ := r.store.ListConnections(did)
		var conn *models.ConnectionProfile
		for _, c := range conns {
			if c.Enabled {
				conn = &c
				break
			}
		}
		if conn == nil {
			out = append(out, map[string]interface{}{"node_id": "local", "device_id": did, "device_name": name,
				"ok": false, "output": "", "error": "无启用的连接配置"})
			continue
		}
		out = append(out, r.execOnConn(conn, command, waitMs, "local", did, name))
	}
	return out
}

func (r *Runner) execRemote(nodeID string, deviceIDs []int64, command string, waitMs int) []map[string]interface{} {
	out := []map[string]interface{}{}
	peer := r.cluster.GetPeer(nodeID)
	if peer == nil || peer.Status != "online" {
		for _, did := range deviceIDs {
			out = append(out, map[string]interface{}{"node_id": nodeID, "device_id": did,
				"ok": false, "output": "", "error": "节点不在线"})
		}
		return out
	}
	// TODO: 调 peer 的批量接口（跨节点）
	for _, did := range deviceIDs {
		out = append(out, map[string]interface{}{"node_id": nodeID, "device_id": did,
			"ok": false, "output": "", "error": "跨节点批量执行待中继实现"})
	}
	return out
}

func (r *Runner) execOnConn(conn *models.ConnectionProfile, command string, waitMs int, nodeID string, deviceID int64, deviceName string) map[string]interface{} {
	// 开临时会话执行
	sess, err := r.manager.Open(conn.ID, conn.Kind, conn.Params, conn.DeviceID, "batch")
	if err != nil {
		return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
			"ok": false, "output": "", "error": err.Error()}
	}
	// 等上线
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sess.Status != "online" {
		if sess.Status == "error" {
			r.manager.Close(sess.ID)
			return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
				"ok": false, "output": "", "error": sess.LastError}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sess.Status != "online" {
		r.manager.Close(sess.ID)
		return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
			"ok": false, "output": "", "error": "会话上线超时"}
	}
	// 写命令 + 收集输出
	client, _ := sess.Subscribe("batch", "批量执行")
	sess.Write("", []byte(command+"\r"))
	output := []byte{}
	timeout := time.After(time.Duration(waitMs) * time.Millisecond)
loop:
	for {
		select {
		case item := <-client.Queue:
			if b, ok := item.([]byte); ok {
				output = append(output, b...)
			}
		case <-timeout:
			break loop
		}
	}
	sess.Unsubscribe("batch")
	r.manager.Close(sess.ID)
	return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
		"ok": true, "output": string(output), "error": ""}
}

// ---------- 定时任务 ----------
type Scheduler struct {
	store   *store.Store
	manager *session.Manager
	cluster *cluster.Cluster
	tasks   map[int64]chan struct{}
}

func NewScheduler(st *store.Store, mgr *session.Manager, cl *cluster.Cluster) *Scheduler {
	return &Scheduler{store: st, manager: mgr, cluster: cl, tasks: map[int64]chan struct{}{}}
}

func (s *Scheduler) Start() {
	tasks, _ := s.store.ListTasks()
	for _, t := range tasks {
		if t.Enabled {
			s.track(t.ID)
		}
	}
}

func (s *Scheduler) track(taskID int64) {
	stop := make(chan struct{})
	s.tasks[taskID] = stop
	go s.loop(taskID, stop)
}

func (s *Scheduler) Untrack(taskID int64) {
	if stop, ok := s.tasks[taskID]; ok {
		close(stop)
		delete(s.tasks, taskID)
	}
}

func (s *Scheduler) loop(taskID int64, stop chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			task, err := s.getTask(taskID)
			if err != nil || task == nil || !task.Enabled {
				return
			}
			shouldRun := task.LastRun == nil ||
				time.Since(*task.LastRun).Seconds() >= float64(task.IntervalS)
			if shouldRun {
				s.runOnce(task)
			}
		}
	}
}

func (s *Scheduler) getTask(id int64) (*models.ScheduledTask, error) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, nil
}

func (s *Scheduler) runOnce(task *models.ScheduledTask) {
	runner := NewRunner(s.store, s.manager, s.cluster)
	steps := []PlaybookStep{}
	for _, t := range task.Targets {
		steps = append(steps, PlaybookStep{
			Targets: []map[string]interface{}{t}, Command: task.Command, WaitMs: task.WaitMs, Parallel: true,
		})
	}
	pb := &Playbook{Name: task.Name, Steps: steps}
	result := runner.Run(pb)
	// 更新历史
	history := task.History
	if history == nil {
		history = []map[string]interface{}{}
	}
	resultJSON, _ := json.Marshal(result)
	var resultMap map[string]interface{}
	json.Unmarshal(resultJSON, &resultMap)
	history = append(history, map[string]interface{}{
		"ts": time.Now().Format(time.RFC3339), "ok": resultMap["ok"], "results": resultMap["steps"],
	})
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	task.History = history
	now := time.Now()
	task.LastRun = &now
	// TODO: store update task
}
