//go:build linux

package monitor

import (
	"github.com/cilium/ebpf/asm"
)

const (
	protoTCP = 6
	protoUDP = 17

	axOffset = 80 // pt_regs.ax on x86_64: kretprobe return value

	keyOffset  = -16 // fp-16: u32 pid | u16 proto | u16 pad
	initOffset = -32 // fp-32: u64 sent | u64 recv

	sentOffset = 0
	recvOffset = 8

	// 单次调用字节上界（64MB）：内核 retval 语义随版本变化，垃圾大值直接丢弃，
	// 否则一次污染能把速率顶到几十 GB/s
	maxPerCall = 64 << 20
)

type probeSpec struct {
	name    string
	proto   uint16
	counter int16
}

var probeSpecs = []probeSpec{
	{"tcp_sendmsg", protoTCP, sentOffset},
	{"tcp_recvmsg", protoTCP, recvOffset},
	{"udp_sendmsg", protoUDP, sentOffset},
	{"udp_recvmsg", protoUDP, recvOffset},
	// TODO: tcp_sendpage is not counted here.
}

func probeInstructions(spec probeSpec, mapFD int) asm.Instructions {
	other := int16(recvOffset)
	if spec.counter == recvOffset {
		other = sentOffset
	}

	return asm.Instructions{
		// retval（R2）>0 且 ≤maxPerCall 才计入：负值=错误码、0=无数据、
		// 超上界=retval 语义异常（新内核/特殊路径），一律丢弃
		asm.LoadMem(asm.R2, asm.R1, axOffset, asm.DWord),
		asm.JSGT.Imm(asm.R2, 0, "positive"),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return(),

		// retval > 0：钳上界，≤maxPerCall 才继续计数（JLE→count），超限丢弃
		asm.Mov.Reg(asm.R6, asm.R2).WithSymbol("positive"),
		asm.Mov.Imm(asm.R7, maxPerCall),
		asm.JLE.Reg(asm.R6, asm.R7, "count"),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return(),

		asm.FnGetCurrentPidTgid.Call().WithSymbol("count"),
		asm.RSh.Imm(asm.R0, 32),
		asm.StoreMem(asm.RFP, keyOffset, asm.R0, asm.Word),
		asm.Mov.Imm(asm.R3, int32(spec.proto)),
		asm.StoreMem(asm.RFP, keyOffset+4, asm.R3, asm.Half),
		asm.Mov.Imm(asm.R3, 0),
		asm.StoreMem(asm.RFP, keyOffset+6, asm.R3, asm.Half),

		asm.LoadMapPtr(asm.R1, mapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, keyOffset),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "create"),

		asm.Mov.Reg(asm.R8, asm.R0),
		asm.LoadMem(asm.R2, asm.R8, spec.counter, asm.DWord),
		asm.Add.Reg(asm.R2, asm.R6),
		asm.StoreMem(asm.R8, spec.counter, asm.R2, asm.DWord),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return(),

		asm.StoreMem(asm.RFP, initOffset+spec.counter, asm.R6, asm.DWord).WithSymbol("create"),
		asm.Mov.Imm(asm.R3, 0),
		asm.StoreMem(asm.RFP, initOffset+other, asm.R3, asm.DWord),
		asm.LoadMapPtr(asm.R1, mapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, keyOffset),
		asm.Mov.Reg(asm.R3, asm.RFP),
		asm.Add.Imm(asm.R3, initOffset),
		asm.Mov.Imm(asm.R4, 0),
		asm.FnMapUpdateElem.Call(),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return(),
	}
}
