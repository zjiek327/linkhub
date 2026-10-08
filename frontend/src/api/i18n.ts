import { createI18n } from 'vue-i18n'
import { settings } from './settings'
import { watchEffect } from 'vue'

const zh = {
  app: { name: '灵枢 LinkHub', mascot: '连连 · 设备连接平台' },
  menu: {
    dashboard: '仪表盘', devices: '设备管理', templates: '模板库',
    automation: '自动化', cluster: '集群管理', settings: '设置', admin: '系统管理', logout: '退出',
  },
  login: {
    username: '用户名', password: '密码', signIn: '登 录',
    welcome: '欢迎', firstRunTip: '初始账号 admin / admin，登录后请立即修改密码',
  },
  common: {
    create: '新建', edit: '编辑', delete: '删除', save: '保存', cancel: '取消',
    confirm: '确定', search: '搜索', refresh: '刷新', close: '关闭', back: '← 返回',
    online: '在线', offline: '离线', status: '状态', actions: '操作', enabled: '启用',
  },
  devices: {
    newDevice: '新建设备', fromTemplate: '从模板创建', batch: '批量执行',
    batchExec: '批量执行命令', batchHint: '向选中的设备下发命令（跨节点自动路由），收集输出窗口。',
    name: '名称', node: '节点', tags: '标签', location: '位置', description: '描述',
    openTerminal: '打开终端', newConnection: '新增连接', connections: '连接配置',
    history: '历史会话', deleteConfirm: '确定删除该设备及其全部连接配置？',
    deleteRemoteConfirm: '剔除该远程设备？（在归属节点上级联删除）',
  },
  terminal: {
    reconnect: '重连', reconnectTip: '会话活着则重连数据流；已断开则用同一连接配置秒开新会话',
    syncSize: '适配大小', syncSizeTip: '串口无窗口尺寸协商，tmux/vim/htop 显示异常时点这里',
    requestInput: '申请输入', releaseControl: '释放控制',
    master: '✏️ 主控', viewer: '👁 旁观', watching: '人在看',
    inputRequest: '✋ 输入权限申请', approve: '同意', deny: '拒绝',
    viewOnlyHint: '旁观模式只读，点「✋ 申请输入」获取权限',
    gotInput: '你已获得输入权限', inputMovedTo: '输入权限已移交给',
    writerFree: '主控已释放，点「✋ 申请输入」即可获得权限',
    denied: '主控拒绝了你的输入申请', released: '已释放控制，其他人可申请输入',
    closeSession: '关闭会话', reconnecting: '重连失败', reconnected: '已重新打开会话',
    disconnected: '连接已断开', reconnectToRecover: '点「↻ 重连」恢复',
  },
  settings: {
    appearance: '外观', themeMode: '主题模式', system: '跟随系统', light: '浅色', dark: '深色',
    terminal: '终端', fontSize: '字号', fontFamily: '字体', theme: '配色主题',
    customBg: '自定义背景', resetTheme: '恢复主题默认', autoSyncSize: '自动同步尺寸',
    autoSyncTip: '窗口变化时自动向远端注入 stty 对齐终端尺寸；登录提示符阶段会注入成用户名',
    preview: '预览', language: '语言', langTip: '设置自动保存',
  },
  cluster: {
    joinNode: '加入节点', discovered: '🔍 发现的节点（自动探测）', nodes: '集群节点',
    joinSelected: '加入选中', ignoreSelected: '忽略选中', approve: '加入', ignore: '忽略',
    remove: '移除', leaveConfirm: '将该节点移出集群？', nodeAddr: '节点地址', clusterToken: '集群令牌',
    tokenPlaceholder: '留空使用本机配置的令牌', approveJoin: '握手加入',
    notEnabled: '集群模式未启用。在各节点后端设置 LINKHUB_CLUSTER_ENABLED=true 与相同的 LINKHUB_CLUSTER_TOKEN 后重启。',
  },
  admin: {
    users: '用户', credentials: '凭证', audit: '审计日志',
    addUser: '新增用户', addCredential: '新增凭证', role: '角色',
    credTip: '密码/私钥加密存储（AES-GCM），SSH 连接配置可引用',
  },
  automation: {
    tasks: '⏰ 定时任务', playbook: '📜 脚本编排（多步骤）', newTask: '新建任务',
    history: '历史', run: '▶ 执行', addStep: '+ 添加步骤', command: '命令', name: '名称',
    interval: '间隔(分钟)', targets: '目标设备', pickDevices: '选设备', allOk: '全部成功',
    someFailed: '存在失败（后续步骤已跳过）',
  },
}

const en: typeof zh = {
  app: { name: 'LinkHub', mascot: 'Lian · Device Connection Platform' },
  menu: {
    dashboard: 'Dashboard', devices: 'Devices', templates: 'Templates',
    automation: 'Automation', cluster: 'Cluster', settings: 'Settings', admin: 'Admin', logout: 'Log out',
  },
  login: {
    username: 'Username', password: 'Password', signIn: 'Sign In',
    welcome: 'Welcome', firstRunTip: 'Initial account admin / admin, change password after login',
  },
  common: {
    create: 'New', edit: 'Edit', delete: 'Delete', save: 'Save', cancel: 'Cancel',
    confirm: 'OK', search: 'Search', refresh: 'Refresh', close: 'Close', back: '← Back',
    online: 'Online', offline: 'Offline', status: 'Status', actions: 'Actions', enabled: 'Enabled',
  },
  devices: {
    newDevice: 'New Device', fromTemplate: 'From Template', batch: 'Batch Exec',
    batchExec: 'Batch Command', batchHint: 'Send command to selected devices (auto-routed cross-node).',
    name: 'Name', node: 'Node', tags: 'Tags', location: 'Location', description: 'Description',
    openTerminal: 'Open Terminal', newConnection: 'New Connection', connections: 'Connections',
    history: 'Session History', deleteConfirm: 'Delete device and all its connections?',
    deleteRemoteConfirm: 'Remove remote device? (cascades on home node)',
  },
  terminal: {
    reconnect: 'Reconnect', reconnectTip: 'Reconnect data stream if alive; reopen new session otherwise',
    syncSize: 'Fit Size', syncSizeTip: 'Serial has no winsize negotiation; click when tmux/vim looks tiny',
    requestInput: 'Request Input', releaseControl: 'Release Control',
    master: '✏️ Master', viewer: '👁 Viewer', watching: 'watching',
    inputRequest: '✋ Input Request', approve: 'Approve', deny: 'Deny',
    viewOnlyHint: 'View-only mode. Click "✋ Request Input" to get write access',
    gotInput: 'You got input permission', inputMovedTo: 'Input moved to',
    writerFree: 'Master released. Click "✋ Request Input" to take over',
    denied: 'Master denied your input request', released: 'Released. Others may request input',
    closeSession: 'Close Session', reconnecting: 'Reconnect failed', reconnected: 'Reopened session',
    disconnected: 'Disconnected', reconnectToRecover: 'click "↻ Reconnect" to recover',
  },
  settings: {
    appearance: 'Appearance', themeMode: 'Theme', system: 'System', light: 'Light', dark: 'Dark',
    terminal: 'Terminal', fontSize: 'Font Size', fontFamily: 'Font', theme: 'Theme',
    customBg: 'Custom BG', resetTheme: 'Reset to theme', autoSyncSize: 'Auto-sync size',
    autoSyncTip: 'Auto-inject stty on window resize; at login prompt it becomes a username',
    preview: 'Preview', language: 'Language', langTip: 'Settings are saved automatically',
  },
  cluster: {
    joinNode: 'Join Node', discovered: '🔍 Discovered Nodes', nodes: 'Cluster Nodes',
    joinSelected: 'Join Selected', ignoreSelected: 'Ignore Selected', approve: 'Join', ignore: 'Ignore',
    remove: 'Remove', leaveConfirm: 'Remove this node from cluster?', nodeAddr: 'Node Address',
    clusterToken: 'Cluster Token', tokenPlaceholder: 'Leave empty to use server token',
    approveJoin: 'Handshake & Join',
    notEnabled: 'Cluster mode is off. Set LINKHUB_CLUSTER_ENABLED=true and a shared LINKHUB_CLUSTER_TOKEN on every node, then restart.',
  },
  admin: {
    users: 'Users', credentials: 'Credentials', audit: 'Audit Log',
    addUser: 'Add User', addCredential: 'Add Credential', role: 'Role',
    credTip: 'Passwords/keys are AES-GCM encrypted; referenced by SSH connections',
  },
  automation: {
    tasks: '⏰ Scheduled Tasks', playbook: '📜 Playbook (multi-step)', newTask: 'New Task',
    history: 'History', run: '▶ Run', addStep: '+ Add Step', command: 'Command', name: 'Name',
    interval: 'Interval (min)', targets: 'Targets', pickDevices: 'Pick Devices', allOk: 'All succeeded',
    someFailed: 'Some failed (subsequent steps skipped)',
  },
}

export const i18n = createI18n({
  legacy: false,
  locale: 'zh',
  fallbackLocale: 'zh',
  messages: { zh, en },
})

/** 当前生效语言（跟随 settings.language：'zh' | 'en' | 'system'） */
export function effectiveLocale(): 'zh' | 'en' {
  const pref = settings.language ?? 'system'
  if (pref === 'zh' || pref === 'en') return pref
  return navigator.language.startsWith('zh') ? 'zh' : 'en'
}

// 跟随设置自动切换（响应式）
watchEffect(() => {
  i18n.global.locale.value = effectiveLocale()
})
