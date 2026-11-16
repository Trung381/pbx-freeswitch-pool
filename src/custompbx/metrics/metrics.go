package metrics

import (
	"custompbx/altData"
	"custompbx/cache"
	"custompbx/dataSourceAdapter"
	"custompbx/mainStruct"
	"custompbx/pbxcache"
	"custompbx/webcache"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/mem"
	"time"
)

func dealwithErr(err error) {
	if err != nil {
		logger.Error("hardware metrics collection failed", "err", err)
		//os.Exit(-1)
	}
}

func GetHardwareData() {
	//runtimeOS := runtime.GOOS
	vmStat, err := mem.VirtualMemory()
	dealwithErr(err)
	diskStat, err := disk.Usage("/")
	dealwithErr(err)
	cpuStat, err := cpu.Info()
	dealwithErr(err)
	percentage, err := cpu.Percent(0, true)
	dealwithErr(err)
	hostStat, err := host.Info()
	dealwithErr(err)
	//interfStat, err := net.Interfaces()
	//dealwithErr(err)
	data := mainStruct.DashboardData{
		Timestamp:    time.Now(),
		Hostname:     hostStat.Hostname,
		OS:           hostStat.OS,
		Platform:     hostStat.Platform,
		CPUModel:     cpuStat[0].ModelName,
		CPUFrequency: cpuStat[0].Mhz,

		DynamicMetrics: mainStruct.DynamicMetrics{
			PercentageDiskUsage:  diskStat.UsedPercent,
			TotalDiscSpace:       diskStat.Total,
			FreeDiskSpace:        diskStat.Free,
			TotalMemory:          vmStat.Total,
			FreeMemory:           vmStat.Free,
			PercentageUsedMemory: vmStat.UsedPercent,
			CoreUtilization:      percentage,
		},
	}
	webcache.DashBoardSetStaticData(data)

}

func UpdateMetrics() {
	tick := time.Tick(1 * time.Second)
	webcache.DashBoardSetSipRegs(cache.GetDomainSipRegsCounter())
	profiles, gateways := altData.GetSofiaProfilesAndGateways()
	profiles = dataSourceAdapter.UpdateSofiaProfileStatuses(profiles)
	gateways = dataSourceAdapter.UpdateSofiaGatewayStatuses(gateways)
	webcache.DashBoardSetSofiaData(profiles, gateways)
	webcache.DashBoardSetCallsCounter(pbxcache.GetChannelsCounter())
	GetHardwareData()

	for {
		select {
		case <-tick:
			GetHardwareData()
			//save to db then
		}
	}
}

/*
2019/08/30 15:00:49 Hostname: debian-01
2019/08/30 15:00:49 OS: linux
2019/08/30 15:00:49 Platform: debian

2019/08/30 15:00:49 Total memory: 1032429568 bytes
2019/08/30 15:00:49 Free memory: 87228416 bytes
2019/08/30 15:00:49 Percentage used memory: 37.80%

2019/08/30 15:00:49 Total disk space: 26288107520 bytes
2019/08/30 15:00:49 Used disk space:   5014605824 bytes
2019/08/30 15:00:49 Free disk space:  19914551296 bytes
2019/08/30 15:00:49 Percentage disk space usage: 20.12%

2019/08/30 15:00:49 CPU index number: 0
2019/08/30 15:00:49 Number of cores: 1
2019/08/30 15:00:49 Model Name: Intel Core Processor (Broadwell, IBRS)
2019/08/30 15:00:49 Speed: 2199.99 MHz
2019/08/30 15:00:49 Current CPU utilization: [0] 100.00%
2019/08/30 15:00:49 Uptime: 2919809
2019/08/30 15:00:49 Number of processes running: 91
2019/08/30 15:00:49 ------------------------------------------------------
2019/08/30 15:00:49 Interface Name: lo
2019/08/30 15:00:49 Interface behavior or flags: up
2019/08/30 15:00:49 Interface behavior or flags: loopback
2019/08/30 15:00:49 IPv6 or IPv4 addresses: {"addr":"127.0.0.1/8"}
2019/08/30 15:00:49 IPv6 or IPv4 addresses: {"addr":"::1/128"}
2019/08/30 15:00:49 ------------------------------------------------------
2019/08/30 15:00:49 Interface Name: eth0
2019/08/30 15:00:49 Hardware(MAC) Address: 00:16:3e:3e:25:e9
2019/08/30 15:00:49 Interface behavior or flags: up
2019/08/30 15:00:49 Interface behavior or flags: broadcast
2019/08/30 15:00:49 Interface behavior or flags: multicast
2019/08/30 15:00:49 IPv6 or IPv4 addresses: {"addr":"185.247.118.201/24"}
2019/08/30 15:00:49 IPv6 or IPv4 addresses: {"addr":"fe80::216:3eff:fe3e:25e9/64"}
*/
