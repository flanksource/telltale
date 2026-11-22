package groups

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The revision 0 prefix of struct task_vm_info from mach/task_info.h.
type taskVMInfo struct {
	VirtualSize, RegionCountAndPageSize, ResidentSize, ResidentSizePeak uint64
	Device, DevicePeak, Internal, InternalPeak                          uint64
	External, ExternalPeak, Reusable, ReusablePeak                      uint64
	PurgeablePmap, PurgeableResident, PurgeableVirtual                  uint64
	Compressed, CompressedPeak, CompressedLifetime                      uint64
}

const taskVMInfoPurgeable = 23

type machMemoryAPI struct {
	self       uint32
	nameForPID func(uint32, int32, *uint32) int32
	info       func(uint32, int32, *taskVMInfo, *uint32) int32
	deallocate func(uint32, uint32) int32
}

var machMemory = sync.OnceValue(func() machMemoryAPI {
	var self func() uint32
	var mach machMemoryAPI
	purego.RegisterLibFunc(&self, purego.RTLD_DEFAULT, "task_self_trap")
	purego.RegisterLibFunc(&mach.nameForPID, purego.RTLD_DEFAULT, "task_name_for_pid")
	purego.RegisterLibFunc(&mach.info, purego.RTLD_DEFAULT, "task_info")
	purego.RegisterLibFunc(&mach.deallocate, purego.RTLD_DEFAULT, "mach_port_deallocate")
	mach.self = self()
	return mach
})

func readCompressedMemory(pid int, reading *Reading) {
	mach := machMemory()
	var port uint32
	if status := mach.nameForPID(mach.self, int32(pid), &port); status != 0 {
		reading.MemoryError = fmt.Sprintf("PID %d compressed memory: Mach task lookup failed (%d)", pid, status)
		return
	}
	var info taskVMInfo
	count := uint32(unsafe.Sizeof(info) / 4)
	status := mach.info(port, taskVMInfoPurgeable, &info, &count)
	if release := mach.deallocate(mach.self, port); release != 0 {
		reading.MemoryError = fmt.Sprintf("PID %d compressed memory: release Mach task (%d)", pid, release)
	} else if status != 0 || count*4 < uint32(unsafe.Offsetof(info.Compressed)+unsafe.Sizeof(info.Compressed)) {
		reading.MemoryError = fmt.Sprintf("PID %d compressed memory: Mach task info returned status %d, %d words", pid, status, count)
	} else {
		reading.CompressedBytes = &info.Compressed
	}
}
