package core

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/aler9/mediamtx/internal/logger"
)

func metric(key string, value int64) string {
	return key + " " + strconv.FormatInt(value, 10) + "\n"
}

type metricsParent interface {
	Log(logger.Level, string, ...interface{})
}

type metrics struct {
	parent metricsParent

	ln           net.Listener
	server       *http.Server
	mutex        sync.Mutex
	pathManager  apiPathManager
	rtspServer   apiRTSPServer
	rtspsServer  apiRTSPServer
	rtmpServer   apiRTMPServer
	hlsServer    apiHLSServer
	webRTCServer apiWebRTCServer
}

func newMetrics(
	address string,
	parent metricsParent,
) (*metrics, error) {
	ln, err := net.Listen(restrictNetwork(restrictNetwork("tcp", address)))
	if err != nil {
		return nil, err
	}

	m := &metrics{
		parent: parent,
		ln:     ln,
	}

	router := gin.New()
	router.SetTrustedProxies(nil)
	router.GET("/metrics", m.onMetrics)

	m.server = &http.Server{
		Handler:  router,
		ErrorLog: log.New(&nilWriter{}, "", 0),
	}

	m.log(logger.Info, "listener opened on "+address)

	go m.server.Serve(m.ln)

	return m, nil
}

func (m *metrics) close() {
	m.log(logger.Info, "listener is closing")
	m.server.Shutdown(context.Background())
	m.ln.Close() // in case Shutdown() is called before Serve()
}

func (m *metrics) log(level logger.Level, format string, args ...interface{}) {
	m.parent.Log(level, "[metrics] "+format, args...)
}

func (m *metrics) onMetrics(ctx *gin.Context) {
	out := ""

	res := m.pathManager.apiPathsList()
	if res.err == nil {
		for name, i := range res.data.Items {
			var state string
			if i.SourceReady {
				state = "ready"
			} else {
				state = "notReady"
			}

			tags := "{name=\"" + name + "\",state=\"" + state + "\"}"
			out += metric("paths"+tags, 1)
			out += metric("paths_bytes_received"+tags, int64(i.BytesReceived))
		}
	}

	if !interfaceIsEmpty(m.hlsServer) {
		res := m.hlsServer.apiMuxersList()
		if res.err == nil {
			for name, i := range res.data.Items {
				tags := "{name=\"" + name + "\"}"
				out += metric("hls_muxers"+tags, 1)
				out += metric("hls_muxers_bytes_sent"+tags, int64(i.BytesSent))
			}
		}
	}

	if !interfaceIsEmpty(m.rtspServer) { //nolint:dupl
		func() {
			res := m.rtspServer.apiConnsList()
			if res.err == nil {
				for id, i := range res.data.Items {
					tags := "{id=\"" + id + "\"}"
					out += metric("rtsp_conns"+tags, 1)
					out += metric("rtsp_conns_bytes_received"+tags, int64(i.BytesReceived))
					out += metric("rtsp_conns_bytes_sent"+tags, int64(i.BytesSent))
				}
			}
		}()

		func() {
			res := m.rtspServer.apiSessionsList()
			if res.err == nil {
				for id, i := range res.data.Items {
					tags := "{id=\"" + id + "\",state=\"" + i.State + "\"}"
					out += metric("rtsp_sessions"+tags, 1)
					out += metric("rtsp_sessions_bytes_received"+tags, int64(i.BytesReceived))
					out += metric("rtsp_sessions_bytes_sent"+tags, int64(i.BytesSent))
				}
			}
		}()
	}

	if !interfaceIsEmpty(m.rtspsServer) { //nolint:dupl
		func() {
			res := m.rtspsServer.apiConnsList()
			if res.err == nil {
				for id, i := range res.data.Items {
					tags := "{id=\"" + id + "\"}"
					out += metric("rtsps_conns"+tags, 1)
					out += metric("rtsps_conns_bytes_received"+tags, int64(i.BytesReceived))
					out += metric("rtsps_conns_bytes_sent"+tags, int64(i.BytesSent))
				}
			}
		}()

		func() {
			res := m.rtspsServer.apiSessionsList()
			if res.err == nil {
				for id, i := range res.data.Items {
					tags := "{id=\"" + id + "\",state=\"" + i.State + "\"}"
					out += metric("rtsps_sessions"+tags, 1)
					out += metric("rtsps_sessions_bytes_received"+tags, int64(i.BytesReceived))
					out += metric("rtsps_sessions_bytes_sent"+tags, int64(i.BytesSent))
				}
			}
		}()
	}

	if !interfaceIsEmpty(m.rtmpServer) {
		res := m.rtmpServer.apiConnsList()
		if res.err == nil {
			for id, i := range res.data.Items {
				tags := "{id=\"" + id + "\",state=\"" + i.State + "\"}"
				out += metric("rtmp_conns"+tags, 1)
				out += metric("rtmp_conns_bytes_received"+tags, int64(i.BytesReceived))
				out += metric("rtmp_conns_bytes_sent"+tags, int64(i.BytesSent))
			}
		}
	}

	if !interfaceIsEmpty(m.webRTCServer) {
		res := m.webRTCServer.apiConnsList()
		if res.err == nil {
			for id, i := range res.data.Items {
				tags := "{id=\"" + id + "\"}"
				out += metric("webrtc_conns"+tags, 1)
				out += metric("webrtc_conns_bytes_received"+tags, int64(i.BytesReceived))
				out += metric("webrtc_conns_bytes_sent"+tags, int64(i.BytesSent))
			}
		}
	}

	ctx.Writer.WriteHeader(http.StatusOK)
	io.WriteString(ctx.Writer, out)
}

// pathManagerSet is called by pathManager.
func (m *metrics) pathManagerSet(s apiPathManager) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.pathManager = s
}

// hlsServerSet is called by hlsServer.
func (m *metrics) hlsServerSet(s apiHLSServer) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.hlsServer = s
}

// rtspServerSet is called by rtspServer (plain).
func (m *metrics) rtspServerSet(s apiRTSPServer) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.rtspServer = s
}

// rtspsServerSet is called by rtspServer (tls).
func (m *metrics) rtspsServerSet(s apiRTSPServer) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.rtspsServer = s
}

// rtmpServerSet is called by rtmpServer.
func (m *metrics) rtmpServerSet(s apiRTMPServer) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.rtmpServer = s
}

// webRTCServerSet is called by webRTCServer.
func (m *metrics) webRTCServerSet(s apiWebRTCServer) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.webRTCServer = s
}
