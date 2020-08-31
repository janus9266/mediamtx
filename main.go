package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/aler9/gortsplib"
	"gopkg.in/alecthomas/kingpin.v2"
)

var Version = "v0.0.0"

const (
	checkPathPeriod = 5 * time.Second
)

type logDestination int

const (
	logDestinationStdout logDestination = iota
	logDestinationFile
)

type program struct {
	conf             *conf
	logFile          *os.File
	metrics          *metrics
	pprof            *pprof
	paths            map[string]*path
	serverRtp        *serverUdp
	serverRtcp       *serverUdp
	serverRtsp       *serverTcp
	clients          map[*client]struct{}
	udpClientsByAddr map[udpClientAddr]*udpClient
	publisherCount   int
	readerCount      int

	metricsGather   chan metricsGatherReq
	clientNew       chan net.Conn
	clientClose     chan *client
	clientDescribe  chan clientDescribeReq
	clientAnnounce  chan clientAnnounceReq
	clientSetupPlay chan clientSetupPlayReq
	clientPlay      chan *client
	clientRecord    chan *client
	clientFrameUdp  chan clientFrameUdpReq
	clientFrameTcp  chan clientFrameTcpReq
	sourceReady     chan *source
	sourceNotReady  chan *source
	sourceFrame     chan sourceFrameReq
	terminate       chan struct{}
	done            chan struct{}
}

func newProgram(args []string, stdin io.Reader) (*program, error) {
	k := kingpin.New("rtsp-simple-server",
		"rtsp-simple-server "+Version+"\n\nRTSP server.")

	argVersion := k.Flag("version", "print version").Bool()
	argConfPath := k.Arg("confpath", "path to a config file. The default is rtsp-simple-server.yml. Use 'stdin' to read config from stdin").Default("rtsp-simple-server.yml").String()

	kingpin.MustParse(k.Parse(args))

	if *argVersion == true {
		fmt.Println(Version)
		os.Exit(0)
	}

	conf, err := loadConf(*argConfPath, stdin)
	if err != nil {
		return nil, err
	}

	p := &program{
		conf:             conf,
		paths:            make(map[string]*path),
		clients:          make(map[*client]struct{}),
		udpClientsByAddr: make(map[udpClientAddr]*udpClient),
		metricsGather:    make(chan metricsGatherReq),
		clientNew:        make(chan net.Conn),
		clientClose:      make(chan *client),
		clientDescribe:   make(chan clientDescribeReq),
		clientAnnounce:   make(chan clientAnnounceReq),
		clientSetupPlay:  make(chan clientSetupPlayReq),
		clientPlay:       make(chan *client),
		clientRecord:     make(chan *client),
		clientFrameUdp:   make(chan clientFrameUdpReq),
		clientFrameTcp:   make(chan clientFrameTcpReq),
		sourceReady:      make(chan *source),
		sourceNotReady:   make(chan *source),
		sourceFrame:      make(chan sourceFrameReq),
		terminate:        make(chan struct{}),
		done:             make(chan struct{}),
	}

	if _, ok := p.conf.logDestinationsParsed[logDestinationFile]; ok {
		p.logFile, err = os.OpenFile(p.conf.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
	}

	p.log("rtsp-simple-server %s", Version)

	if conf.Metrics {
		p.metrics, err = newMetrics(p)
		if err != nil {
			return nil, err
		}
	}

	if conf.Pprof {
		p.pprof, err = newPprof(p)
		if err != nil {
			return nil, err
		}
	}

	for name, confp := range conf.Paths {
		if name == "all" {
			continue
		}
		p.paths[name] = newPath(p, name, confp, true)
	}

	if _, ok := conf.protocolsParsed[gortsplib.StreamProtocolUdp]; ok {
		p.serverRtp, err = newServerUdp(p, conf.RtpPort, gortsplib.StreamTypeRtp)
		if err != nil {
			return nil, err
		}

		p.serverRtcp, err = newServerUdp(p, conf.RtcpPort, gortsplib.StreamTypeRtcp)
		if err != nil {
			return nil, err
		}
	}

	p.serverRtsp, err = newServerTcp(p)
	if err != nil {
		return nil, err
	}

	go p.run()

	return p, nil
}

func (p *program) log(format string, args ...interface{}) {
	line := fmt.Sprintf("[%d/%d/%d] "+format, append([]interface{}{len(p.clients),
		p.publisherCount, p.readerCount}, args...)...)

	if _, ok := p.conf.logDestinationsParsed[logDestinationStdout]; ok {
		log.Println(line)
	}

	if _, ok := p.conf.logDestinationsParsed[logDestinationFile]; ok {
		p.logFile.WriteString(line + "\n")
	}
}

func (p *program) run() {
	if p.metrics != nil {
		go p.metrics.run()
	}

	if p.pprof != nil {
		go p.pprof.run()
	}

	if p.serverRtp != nil {
		go p.serverRtp.run()
	}

	if p.serverRtcp != nil {
		go p.serverRtcp.run()
	}

	go p.serverRtsp.run()

	for _, p := range p.paths {
		p.onInit()
	}

	checkPathsTicker := time.NewTicker(checkPathPeriod)
	defer checkPathsTicker.Stop()

outer:
	for {
		select {
		case <-checkPathsTicker.C:
			for _, path := range p.paths {
				path.onCheck()
			}

		case req := <-p.metricsGather:
			req.res <- &metricsData{
				clientCount:    len(p.clients),
				publisherCount: p.publisherCount,
				readerCount:    p.readerCount,
			}

		case conn := <-p.clientNew:
			c := newClient(p, conn)
			p.clients[c] = struct{}{}
			c.log("connected")

		case client := <-p.clientClose:
			if _, ok := p.clients[client]; !ok {
				continue
			}
			client.close()

		case req := <-p.clientDescribe:
			// create path if not exist
			if _, ok := p.paths[req.pathName]; !ok {
				p.paths[req.pathName] = newPath(p, req.pathName, p.findConfForPathName(req.pathName), false)
			}

			p.paths[req.pathName].onDescribe(req.client)

		case req := <-p.clientAnnounce:
			// create path if not exist
			if path, ok := p.paths[req.pathName]; !ok {
				p.paths[req.pathName] = newPath(p, req.pathName, p.findConfForPathName(req.pathName), false)

			} else {
				if path.publisher != nil {
					req.res <- fmt.Errorf("someone is already publishing on path '%s'", req.pathName)
					continue
				}
			}

			p.paths[req.pathName].publisher = req.client
			p.paths[req.pathName].publisherSdpText = req.sdpText
			p.paths[req.pathName].publisherSdpParsed = req.sdpParsed

			req.client.path = p.paths[req.pathName]
			req.client.state = clientStatePreRecord
			req.res <- nil

		case req := <-p.clientSetupPlay:
			path, ok := p.paths[req.pathName]
			if !ok || !path.publisherReady {
				req.res <- fmt.Errorf("no one is publishing on path '%s'", req.pathName)
				continue
			}

			if req.trackId >= len(path.publisherSdpParsed.MediaDescriptions) {
				req.res <- fmt.Errorf("track %d does not exist", req.trackId)
				continue
			}

			req.client.path = path
			req.client.state = clientStatePrePlay
			req.res <- nil

		case client := <-p.clientPlay:
			p.readerCount += 1
			client.state = clientStatePlay

		case client := <-p.clientRecord:
			p.publisherCount += 1
			client.state = clientStateRecord

			if client.streamProtocol == gortsplib.StreamProtocolUdp {
				for trackId, track := range client.streamTracks {
					key := makeUdpClientAddr(client.ip(), track.rtpPort)
					p.udpClientsByAddr[key] = &udpClient{
						client:     client,
						trackId:    trackId,
						streamType: gortsplib.StreamTypeRtp,
					}

					key = makeUdpClientAddr(client.ip(), track.rtcpPort)
					p.udpClientsByAddr[key] = &udpClient{
						client:     client,
						trackId:    trackId,
						streamType: gortsplib.StreamTypeRtcp,
					}
				}
			}

			client.path.onPublisherSetReady()

		case req := <-p.clientFrameUdp:
			pub, ok := p.udpClientsByAddr[makeUdpClientAddr(req.addr.IP, req.addr.Port)]
			if !ok {
				continue
			}

			// client sent RTP on RTCP port or vice-versa
			if pub.streamType != req.streamType {
				continue
			}

			pub.client.rtcpReceivers[pub.trackId].OnFrame(req.streamType, req.buf)
			p.forwardFrame(pub.client.path, pub.trackId, req.streamType, req.buf)

		case req := <-p.clientFrameTcp:
			p.forwardFrame(req.path, req.trackId, req.streamType, req.buf)

		case source := <-p.sourceReady:
			source.log("ready")
			source.path.onPublisherSetReady()

		case source := <-p.sourceNotReady:
			source.log("not ready")
			source.path.onPublisherSetNotReady()

		case req := <-p.sourceFrame:
			p.forwardFrame(req.source.path, req.trackId, req.streamType, req.buf)

		case <-p.terminate:
			break outer
		}
	}

	go func() {
		for {
			select {
			case req, ok := <-p.metricsGather:
				if !ok {
					return
				}
				req.res <- nil

			case <-p.clientNew:
			case <-p.clientClose:
			case <-p.clientDescribe:

			case req := <-p.clientAnnounce:
				req.res <- fmt.Errorf("terminated")

			case req := <-p.clientSetupPlay:
				req.res <- fmt.Errorf("terminated")

			case <-p.clientPlay:
			case <-p.clientRecord:
			case <-p.clientFrameUdp:
			case <-p.clientFrameTcp:
			case <-p.sourceReady:
			case <-p.sourceNotReady:
			case <-p.sourceFrame:
			}
		}
	}()

	for _, p := range p.paths {
		p.onClose()
	}

	p.serverRtsp.close()

	if p.serverRtcp != nil {
		p.serverRtcp.close()
	}

	if p.serverRtp != nil {
		p.serverRtp.close()
	}

	for c := range p.clients {
		c.close()
		<-c.done
	}

	if p.metrics != nil {
		p.metrics.close()
	}

	if p.pprof != nil {
		p.pprof.close()
	}

	if p.logFile != nil {
		p.logFile.Close()
	}

	close(p.metricsGather)
	close(p.clientNew)
	close(p.clientClose)
	close(p.clientDescribe)
	close(p.clientAnnounce)
	close(p.clientSetupPlay)
	close(p.clientPlay)
	close(p.clientRecord)
	close(p.clientFrameUdp)
	close(p.clientFrameTcp)
	close(p.sourceReady)
	close(p.sourceNotReady)
	close(p.sourceFrame)
	close(p.done)
}

func (p *program) close() {
	close(p.terminate)
	<-p.done
}

func (p *program) findConfForPathName(name string) *confPath {
	if confp, ok := p.conf.Paths[name]; ok {
		return confp
	}

	if confp, ok := p.conf.Paths["all"]; ok {
		return confp
	}

	return nil
}

func (p *program) forwardFrame(path *path, trackId int, streamType gortsplib.StreamType, frame []byte) {
	for c := range p.clients {
		if c.path != path ||
			c.state != clientStatePlay {
			continue
		}

		track, ok := c.streamTracks[trackId]
		if !ok {
			continue
		}

		if c.streamProtocol == gortsplib.StreamProtocolUdp {
			if streamType == gortsplib.StreamTypeRtp {
				p.serverRtp.write(&udpAddrBufPair{
					addr: &net.UDPAddr{
						IP:   c.ip(),
						Zone: c.zone(),
						Port: track.rtpPort,
					},
					buf: frame,
				})
			} else {
				p.serverRtcp.write(&udpAddrBufPair{
					addr: &net.UDPAddr{
						IP:   c.ip(),
						Zone: c.zone(),
						Port: track.rtcpPort,
					},
					buf: frame,
				})
			}

		} else {
			c.tcpFrame <- &gortsplib.InterleavedFrame{
				TrackId:    trackId,
				StreamType: streamType,
				Content:    frame,
			}
		}
	}
}

func main() {
	_, err := newProgram(os.Args[1:], os.Stdin)
	if err != nil {
		log.Fatal("ERR: ", err)
	}

	select {}
}
