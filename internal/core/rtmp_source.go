package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v3/pkg/formats"
	"github.com/bluenviron/gortsplib/v3/pkg/media"

	"github.com/aler9/mediamtx/internal/conf"
	"github.com/aler9/mediamtx/internal/logger"
	"github.com/aler9/mediamtx/internal/rtmp"
	"github.com/aler9/mediamtx/internal/rtmp/message"
)

type rtmpSourceParent interface {
	log(logger.Level, string, ...interface{})
	sourceStaticImplSetReady(req pathSourceStaticSetReadyReq) pathSourceStaticSetReadyRes
	sourceStaticImplSetNotReady(req pathSourceStaticSetNotReadyReq)
}

type rtmpSource struct {
	readTimeout  conf.StringDuration
	writeTimeout conf.StringDuration
	parent       rtmpSourceParent
}

func newRTMPSource(
	readTimeout conf.StringDuration,
	writeTimeout conf.StringDuration,
	parent rtmpSourceParent,
) *rtmpSource {
	return &rtmpSource{
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		parent:       parent,
	}
}

func (s *rtmpSource) Log(level logger.Level, format string, args ...interface{}) {
	s.parent.log(level, "[rtmp source] "+format, args...)
}

// run implements sourceStaticImpl.
func (s *rtmpSource) run(ctx context.Context, cnf *conf.PathConf, reloadConf chan *conf.PathConf) error {
	s.Log(logger.Debug, "connecting")

	u, err := url.Parse(cnf.Source)
	if err != nil {
		return err
	}

	// add default port
	_, _, err = net.SplitHostPort(u.Host)
	if err != nil {
		u.Host = net.JoinHostPort(u.Host, "1935")
	}

	ctx2, cancel2 := context.WithTimeout(ctx, time.Duration(s.readTimeout))
	defer cancel2()

	nconn, err := func() (net.Conn, error) {
		if u.Scheme == "rtmp" {
			return (&net.Dialer{}).DialContext(ctx2, "tcp", u.Host)
		}

		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
			VerifyConnection: func(cs tls.ConnectionState) error {
				h := sha256.New()
				h.Write(cs.PeerCertificates[0].Raw)
				hstr := hex.EncodeToString(h.Sum(nil))
				fingerprintLower := strings.ToLower(cnf.SourceFingerprint)

				if hstr != fingerprintLower {
					return fmt.Errorf("server fingerprint do not match: expected %s, got %s",
						fingerprintLower, hstr)
				}

				return nil
			},
		}

		return (&tls.Dialer{Config: tlsConfig}).DialContext(ctx2, "tcp", u.Host)
	}()
	if err != nil {
		return err
	}

	conn := rtmp.NewConn(nconn)

	readDone := make(chan error)
	go func() {
		readDone <- func() error {
			nconn.SetReadDeadline(time.Now().Add(time.Duration(s.readTimeout)))
			nconn.SetWriteDeadline(time.Now().Add(time.Duration(s.writeTimeout)))
			err = conn.InitializeClient(u, false)
			if err != nil {
				return err
			}

			nconn.SetWriteDeadline(time.Time{})
			nconn.SetReadDeadline(time.Now().Add(time.Duration(s.readTimeout)))
			videoFormat, audioFormat, err := conn.ReadTracks()
			if err != nil {
				return err
			}

			if _, ok := videoFormat.(*formats.H265); ok {
				return fmt.Errorf("proxying H265 streams with RTMP is not supported")
			}

			var medias media.Medias
			var videoMedia *media.Media
			var audioMedia *media.Media

			if videoFormat != nil {
				videoMedia = &media.Media{
					Type:    media.TypeVideo,
					Formats: []formats.Format{videoFormat},
				}
				medias = append(medias, videoMedia)
			}

			if audioFormat != nil {
				audioMedia = &media.Media{
					Type:    media.TypeAudio,
					Formats: []formats.Format{audioFormat},
				}
				medias = append(medias, audioMedia)
			}

			res := s.parent.sourceStaticImplSetReady(pathSourceStaticSetReadyReq{
				medias:             medias,
				generateRTPPackets: true,
			})
			if res.err != nil {
				return res.err
			}

			s.Log(logger.Info, "ready: %s", sourceMediaInfo(medias))

			defer func() {
				s.parent.sourceStaticImplSetNotReady(pathSourceStaticSetNotReadyReq{})
			}()

			videoWriteFunc := getRTMPWriteFunc(videoMedia, videoFormat, res.stream)
			audioWriteFunc := getRTMPWriteFunc(audioMedia, audioFormat, res.stream)

			// disable write deadline to allow outgoing acknowledges
			nconn.SetWriteDeadline(time.Time{})

			for {
				nconn.SetReadDeadline(time.Now().Add(time.Duration(s.readTimeout)))
				msg, err := conn.ReadMessage()
				if err != nil {
					return err
				}

				switch tmsg := msg.(type) {
				case *message.MsgVideo:
					if videoFormat == nil {
						return fmt.Errorf("received an H264 packet, but track is not set up")
					}

					err := videoWriteFunc(tmsg)
					if err != nil {
						s.Log(logger.Warn, "%v", err)
					}

				case *message.MsgAudio:
					if audioFormat == nil {
						return fmt.Errorf("received an AAC packet, but track is not set up")
					}

					err := audioWriteFunc(tmsg)
					if err != nil {
						s.Log(logger.Warn, "%v", err)
					}
				}
			}
		}()
	}()

	for {
		select {
		case err := <-readDone:
			nconn.Close()
			return err

		case <-reloadConf:

		case <-ctx.Done():
			nconn.Close()
			<-readDone
			return nil
		}
	}
}

// apiSourceDescribe implements sourceStaticImpl.
func (*rtmpSource) apiSourceDescribe() interface{} {
	return struct {
		Type string `json:"type"`
	}{"rtmpSource"}
}
