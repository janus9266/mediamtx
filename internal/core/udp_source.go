package core

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/bluenviron/gortsplib/v3/pkg/formats"
	"github.com/bluenviron/gortsplib/v3/pkg/media"
	"github.com/bluenviron/mediacommon/pkg/formats/mpegts"
	"golang.org/x/net/ipv4"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/formatprocessor"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
)

const (
	multicastTTL = 16
	udpMTU       = 1472
)

func joinMulticastGroupOnAtLeastOneInterface(p *ipv4.PacketConn, listenIP net.IP) error {
	intfs, err := net.Interfaces()
	if err != nil {
		return err
	}

	success := false

	for _, intf := range intfs {
		if (intf.Flags & net.FlagMulticast) != 0 {
			err := p.JoinGroup(&intf, &net.UDPAddr{IP: listenIP})
			if err == nil {
				success = true
			}
		}
	}

	if !success {
		return fmt.Errorf("unable to activate multicast on any network interface")
	}

	return nil
}

type packetConnReader struct {
	pc net.PacketConn
}

func newPacketConnReader(pc net.PacketConn) *packetConnReader {
	return &packetConnReader{
		pc: pc,
	}
}

func (r *packetConnReader) Read(p []byte) (int, error) {
	n, _, err := r.pc.ReadFrom(p)
	return n, err
}

type udpSourceParent interface {
	logger.Writer
	sourceStaticImplSetReady(req pathSourceStaticSetReadyReq) pathSourceStaticSetReadyRes
	sourceStaticImplSetNotReady(req pathSourceStaticSetNotReadyReq)
}

type udpSource struct {
	readTimeout conf.StringDuration
	parent      udpSourceParent
}

func newUDPSource(
	readTimeout conf.StringDuration,
	parent udpSourceParent,
) *udpSource {
	return &udpSource{
		readTimeout: readTimeout,
		parent:      parent,
	}
}

func (s *udpSource) Log(level logger.Level, format string, args ...interface{}) {
	s.parent.Log(level, "[udp source] "+format, args...)
}

// run implements sourceStaticImpl.
func (s *udpSource) run(ctx context.Context, cnf *conf.PathConf, _ chan *conf.PathConf) error {
	s.Log(logger.Debug, "connecting")

	hostPort := cnf.Source[len("udp://"):]

	pc, err := net.ListenPacket(restrictNetwork("udp", hostPort))
	if err != nil {
		return err
	}
	defer pc.Close()

	host, _, _ := net.SplitHostPort(hostPort)
	ip := net.ParseIP(host)

	if ip.IsMulticast() {
		p := ipv4.NewPacketConn(pc)

		err = p.SetMulticastTTL(multicastTTL)
		if err != nil {
			return err
		}

		err = joinMulticastGroupOnAtLeastOneInterface(p, ip)
		if err != nil {
			return err
		}
	}

	readerErr := make(chan error)

	go func() {
		readerErr <- s.runReader(pc)
	}()

	select {
	case err := <-readerErr:
		return err

	case <-ctx.Done():
		pc.Close()
		<-readerErr
		return fmt.Errorf("terminated")
	}
}

func (s *udpSource) runReader(pc net.PacketConn) error {
	pc.SetReadDeadline(time.Now().Add(time.Duration(s.readTimeout)))
	r, err := mpegts.NewReader(newMPEGTSBufferedReader(newPacketConnReader(pc)))
	if err != nil {
		return err
	}

	var medias media.Medias
	var stream *stream.Stream

	var td *mpegts.TimeDecoder
	decodeTime := func(t int64) time.Duration {
		if td == nil {
			td = mpegts.NewTimeDecoder(t)
		}
		return td.Decode(t)
	}

	for _, track := range r.Tracks() {
		var medi *media.Media

		switch tcodec := track.Codec.(type) {
		case *mpegts.CodecH264:
			medi = &media.Media{
				Type: media.TypeVideo,
				Formats: []formats.Format{&formats.H264{
					PayloadTyp:        96,
					PacketizationMode: 1,
				}},
			}

			r.OnDataH26x(track, func(pts int64, _ int64, au [][]byte) error {
				stream.WriteUnit(medi, medi.Formats[0], &formatprocessor.UnitH264{
					BaseUnit: formatprocessor.BaseUnit{
						NTP: time.Now(),
					},
					PTS: decodeTime(pts),
					AU:  au,
				})
				return nil
			})

		case *mpegts.CodecH265:
			medi = &media.Media{
				Type: media.TypeVideo,
				Formats: []formats.Format{&formats.H265{
					PayloadTyp: 96,
				}},
			}

			r.OnDataH26x(track, func(pts int64, _ int64, au [][]byte) error {
				stream.WriteUnit(medi, medi.Formats[0], &formatprocessor.UnitH265{
					BaseUnit: formatprocessor.BaseUnit{
						NTP: time.Now(),
					},
					PTS: decodeTime(pts),
					AU:  au,
				})
				return nil
			})

		case *mpegts.CodecMPEG4Audio:
			medi = &media.Media{
				Type: media.TypeAudio,
				Formats: []formats.Format{&formats.MPEG4Audio{
					PayloadTyp:       96,
					SizeLength:       13,
					IndexLength:      3,
					IndexDeltaLength: 3,
					Config:           &tcodec.Config,
				}},
			}

			r.OnDataMPEG4Audio(track, func(pts int64, _ int64, aus [][]byte) error {
				stream.WriteUnit(medi, medi.Formats[0], &formatprocessor.UnitMPEG4AudioGeneric{
					BaseUnit: formatprocessor.BaseUnit{
						NTP: time.Now(),
					},
					PTS: decodeTime(pts),
					AUs: aus,
				})
				return nil
			})

		case *mpegts.CodecOpus:
			medi = &media.Media{
				Type: media.TypeAudio,
				Formats: []formats.Format{&formats.Opus{
					PayloadTyp: 96,
					IsStereo:   (tcodec.ChannelCount == 2),
				}},
			}

			r.OnDataOpus(track, func(pts int64, _ int64, packets [][]byte) error {
				stream.WriteUnit(medi, medi.Formats[0], &formatprocessor.UnitOpus{
					BaseUnit: formatprocessor.BaseUnit{
						NTP: time.Now(),
					},
					PTS:     decodeTime(pts),
					Packets: packets,
				})
				return nil
			})
		}

		medias = append(medias, medi)
	}

	res := s.parent.sourceStaticImplSetReady(pathSourceStaticSetReadyReq{
		medias:             medias,
		generateRTPPackets: true,
	})
	if res.err != nil {
		return res.err
	}

	defer s.parent.sourceStaticImplSetNotReady(pathSourceStaticSetNotReadyReq{})

	s.Log(logger.Info, "ready: %s", sourceMediaInfo(medias))

	stream = res.stream

	for {
		pc.SetReadDeadline(time.Now().Add(time.Duration(s.readTimeout)))
		err := r.Read()
		if err != nil {
			return err
		}
	}
}

// apiSourceDescribe implements sourceStaticImpl.
func (*udpSource) apiSourceDescribe() pathAPISourceOrReader {
	return pathAPISourceOrReader{
		Type: "udpSource",
		ID:   "",
	}
}
