package hepHandler

import (
	"custompbx/cfg"
	"custompbx/promexp"
	"fmt"
	"github.com/custompbx/hepparser"
	"net"
	"time"
)

const maxPktLen = 8192

func StartHepListener(DBSaveHandler func(hep []*hepparser.HEP, instanceId int64) error, instanceId int64) {
	if cfg.CustomPbx.Fs.HEPCollector.Host == "" || cfg.CustomPbx.Fs.HEPCollector.Port < 1024 || cfg.CustomPbx.Fs.HEPCollector.Port > 65535 {
		logger.Info("HEP collecting disabled")
		return
	}

	logger.Info("HEP collecting started")
	ua, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.CustomPbx.Fs.HEPCollector.Host, cfg.CustomPbx.Fs.HEPCollector.Port))
	if err != nil {
		logger.Error("StartHepListener failed", "err", err)
		return
	}

	uc, err := net.ListenUDP("udp", ua)
	if err != nil {
		logger.Error("StartHepListener failed", "err", err)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			logger.Error("recovered panic in StartHepListener", "panic", fmt.Sprint(r))
		}
		logger.Info("stopping UDP listener", "addr", uc.LocalAddr().String())
		uc.Close()
	}()

	HEPChannel := make(chan *hepparser.HEP, 40000)
	go HEPSaver(DBSaveHandler, HEPChannel, instanceId)

	for {
		//uc.SetReadDeadline(time.Now().Add(1e9))
		buf := make([]byte, maxPktLen)
		n, err := uc.Read(buf)
		if err != nil {
			//if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
			//	continue
			//} else {
			logger.Error("HEP UDP read failed, listener stops", "err", err)
			return
			//}
		} else if n > maxPktLen {
			logger.Warn("HEP packet too big", "bytes", n)
			continue
		}
		go ParseAndSaveHEP(buf[:n], HEPChannel)
	}
}

func HEPSaver(SaveHEPPackets func(hep []*hepparser.HEP, instanceId int64) error, HEPChannel chan *hepparser.HEP, instanceId int64) {
	chuck := 50
	collector := make([]*hepparser.HEP, 0, chuck)
	tick := time.Tick(300 * time.Millisecond)

	for {
		select {
		case hep := <-HEPChannel:
			collector = append(collector, hep)
			if len(collector) < chuck {
				continue
			}
			go SaveHEPPackets(collector, instanceId)
			collector = []*hepparser.HEP{}
		case <-tick:
			if len(collector) == 0 {
				continue
			}
			go SaveHEPPackets(collector, instanceId)
			collector = []*hepparser.HEP{}
		}
	}
}

func ParseAndSaveHEP(packet []byte, HEPChannel chan *hepparser.HEP) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("recovered panic in ParseAndSaveHEP", "panic", fmt.Sprint(r))
		}
	}()

	hepPacket, err := hepparser.DecodeHEP(packet)
	if err != nil {
		promexp.ObserveHEP("decode_error")
		logger.Error("ParseAndSaveHEP failed", "err", err)
		return
	}
	promexp.ObserveHEP("received")
	HEPChannel <- hepPacket
}
