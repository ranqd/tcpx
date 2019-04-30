package tcpx

import (
	"encoding/json"
	"fmt"
	"github.com/fwhezfwhez/errorx"
	"io"
	"reflect"

	"net"
)

// OnMessage and mux are opposite.
// When OnMessage is not nil, users should deal will ctx.Stream themselves.
// When OnMessage is nil, program will handle ctx.Stream via mux routing by messageID
type TcpX struct {
	OnConnect func(ctx *Context)
	OnMessage func(ctx *Context)
	OnClose   func(ctx *Context)
	Mux       *Mux
	Packx     *Packx
}

// new an tcpx srv instance
func NewTcpX(marshaller Marshaller) *TcpX {
	return &TcpX{
		Packx: NewPackx(marshaller),
		Mux:   NewMux(),
	}
}

// Clone a same tcpx without interfering the former
func (tcpx *TcpX) Clone() TcpX {
	b, e := json.Marshal(tcpx)
	if e != nil {
		panic(e)
	}
	var tmp TcpX
	e = json.Unmarshal(b, &tmp)
	if e != nil {
		panic(e)
	}
	return tmp
}

// Middleware typed 'AnchorTypedMiddleware'.
// Add middlewares ruled by (string , func(c *Context),string , func(c *Context),string , func(c *Context)...).
// Middlewares will be added with an indexed key, which is used to unUse this middleware.
// Each middleware added will be well set an anchor index, when UnUse this middleware, its expire_anchor_index will be well set too.
func (tcpx *TcpX) Use(mids ... interface{}) {
	if tcpx.Mux == nil {
		tcpx.Mux = NewMux()
	}

	if len(mids)%2 != 0 {
		panic(errorx.NewFromStringf("tcpx.Use(mids ...),'mids' should show in pairs,but got length(mids) %d", len(mids)))
	}
	var middlewareKey string
	var ok bool
	var middleware func(c *Context)

	var middlewareAnchor MiddlewareAnchor
	for i := 0; i < len(mids)-1; i += 2 {
		j := i + 1
		middlewareKey, ok = mids[i].(string)
		if !ok {
			panic(errorx.NewFromStringf("tcpx.Use(mids ...), 'mids' index '%d' should be string key type but got %v", i, mids[i]))
		}
		middleware, ok = mids[j].(func(c *Context))
		if !ok {
			panic(errorx.NewFromStringf("tcpx.Use(mids ...), 'mids' index '%d' should be func(c *tcpx.Context) type but got %s", j, reflect.TypeOf(mids[j]).Kind().String()))
		}
		middlewareAnchor.Middleware = middleware
		middlewareAnchor.MiddlewareKey = middlewareKey
		middlewareAnchor.AnchorIndex = tcpx.Mux.CurrentAnchorIndex()
		middlewareAnchor.ExpireAnchorIndex = NOT_EXPIRE

		tcpx.Mux.AddMiddlewareAnchor(middlewareAnchor)

	}
}

// UnUse an middleware.
// a unused middleware will expired among handlers added after it.For example:
//
// 	srv := tcpx.NewTcpX(tcpx.JsonMarshaller{})
//  srv.Use("middleware1", Middleware1, "middleware2", Middleware2)
//	srv.AddHandler(1, SayHello)
//	srv.UnUse("middleware2")
//	srv.AddHandler(3, SayGoodBye)
//
// middleware1 and middleware2 will both work to handler 'SayHello'.
// middleware1 will work to handler 'SayGoodBye' but middleware2 will not work to handler 'SayGoodBye'
func (tcpx *TcpX) UnUse(middlewareKeys ...string) {
	var middlewareAnchor MiddlewareAnchor
	var ok bool
	for _, k := range middlewareKeys {
		if middlewareAnchor, ok = tcpx.Mux.MiddlewareAnchorMap[k]; !ok {
			panic(errorx.NewFromStringf("middlewareKey '%s' not found in mux.MiddlewareAnchorMap", k))
		}
		middlewareAnchor.ExpireAnchorIndex = tcpx.Mux.CurrentAnchorIndex()
		tcpx.Mux.ReplaceMiddlewareAnchor(middlewareAnchor)
	}
}

// Middleware typed 'GlobalTypedMiddleware'.
// GlobalMiddleware will work to all handlers.
func (tcpx *TcpX) UseGlobal(mids ...func(c *Context)) {
	if tcpx.Mux == nil {
		tcpx.Mux = NewMux()
	}
	tcpx.Mux.AddGlobalMiddleware(mids ...)
}

// Middleware typed 'SelfRelatedTypedMiddleware'.
// Add handlers routing by messageID
func (tcpx *TcpX) AddHandler(messageID int32, handlers ... func(ctx *Context)) {
	if len(handlers) <= 0 {
		panic(errorx.NewFromStringf("handlers should more than 1 but got %d", len(handlers)))
	}
	if len(handlers) > 1 {
		tcpx.Mux.AddMessageIDSelfMiddleware(messageID, handlers[:len(handlers)-1]...)
	}

	f := handlers[len(handlers)-1]
	if tcpx.Mux == nil {
		tcpx.Mux = NewMux()
	}
	tcpx.Mux.AddHandleFunc(messageID, f)
	var messageIDAnchor MessageIDAnchor
	messageIDAnchor.MessageID = messageID
	messageIDAnchor.AnchorIndex = tcpx.Mux.CurrentAnchorIndex()
	tcpx.Mux.AddMessageIDAnchor(messageIDAnchor)
}

// Start to listen.
// Serve can decode stream generated by packx.
// Support tcp and udp
func (tcpx *TcpX) ListenAndServe(network, addr string) error {
	if In(network, []string{"tcp", "tcp4", "tcp6", "unix", "unixpacket"}) {
		return tcpx.ListenAndServeTCP(network, addr)
	}
	if In(network, []string{"udp", "udp4", "udp6", "unixgram", "ip%"}) {
		return tcpx.ListenAndServeUDP(network, addr)
	}
	return errorx.NewFromStringf("'network' doesn't support '%s'", network)
}

// tcp
func (tcpx *TcpX) ListenAndServeTCP(network, addr string) error {
	defer func() {
		if e := recover(); e != nil {
			Logger.Println(fmt.Sprintf("recover from panic %v", e))
		}
	}()
	listener, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			Logger.Println(err.Error())
			continue
		}
		ctx := NewContext(conn, tcpx.Packx.Marshaller)
		if tcpx.OnConnect != nil {
			tcpx.OnConnect(ctx)
		}
		go func(ctx *Context, tcpx *TcpX) {
			defer func() {
				if e := recover(); e != nil {
					Logger.Println(fmt.Sprintf("recover from panic %v", e))
				}
			}()
			defer ctx.Conn.Close()
			if tcpx.OnClose != nil {
				defer tcpx.OnClose(ctx)
			}
			var e error
			for {
				ctx.Stream, e = ctx.Packx.FirstBlockOf(ctx.Conn)
				if e != nil {
					if e == io.EOF {
						break
					}
					Logger.Println(e)
					break
				}

				// Since ctx.handlers and ctx.offset will change per request, cannot take this function as a new routine,
				// or ctx.offset and ctx.handler will get dirty
				func(ctx *Context, tcpx *TcpX) {
					if tcpx.OnMessage != nil {
						// tcpx.Mux.execAllMiddlewares(ctx)
						//tcpx.OnMessage(ctx)
						if ctx.handlers == nil {
							ctx.handlers = make([]func(c *Context), 0, 10)
						}
						ctx.handlers = append(ctx.handlers, tcpx.Mux.GlobalMiddlewares...)
						for _, v := range tcpx.Mux.MiddlewareAnchorMap {
							ctx.handlers = append(ctx.handlers, v.Middleware)
						}
						ctx.handlers = append(ctx.handlers, tcpx.OnMessage)
						if len(ctx.handlers) > 0 {
							ctx.Next()
						}
						ctx.Reset()
					} else {
						messageID, e := tcpx.Packx.MessageIDOf(ctx.Stream)
						if e != nil {
							Logger.Println(errorx.Wrap(e).Error())
							return
						}
						handler, ok := tcpx.Mux.Handlers[messageID]
						if !ok {
							Logger.Println(fmt.Sprintf("messageID %d handler not found", messageID))
							return
						}

						//handler(ctx)

						if ctx.handlers == nil {
							ctx.handlers = make([]func(c *Context), 0, 10)
						}

						// global middleware
						ctx.handlers = append(ctx.handlers, tcpx.Mux.GlobalMiddlewares...)
						// anchor middleware
						messageIDAnchorIndex := tcpx.Mux.AnchorIndexOfMessageID(messageID)
						for _, v := range tcpx.Mux.MiddlewareAnchorMap {
							if messageIDAnchorIndex > v.AnchorIndex && messageIDAnchorIndex <= v.ExpireAnchorIndex {
								ctx.handlers = append(ctx.handlers, v.Middleware)
							}
						}
						// self-related middleware
						ctx.handlers = append(ctx.handlers, tcpx.Mux.MessageIDSelfMiddleware[messageID]...)
						// handler
						ctx.handlers = append(ctx.handlers, handler)

						if len(ctx.handlers) > 0 {
							ctx.Next()
						}
						ctx.Reset()
					}
				}(ctx, tcpx)
				continue
			}
		}(ctx, tcpx)
	}
}

// udp
func (tcpx *TcpX) ListenAndServeUDP(network, addr string, maxBufferSize ...int) error {
	if len(maxBufferSize) > 1 {
		panic(errorx.NewFromStringf("'tcpx.ListenAndServeUDP''s maxBufferSize should has length less by 1 but got %d", len(maxBufferSize)))
	}

	conn, err := net.ListenPacket(network, addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	// listen to incoming udp packets
	go func(conn net.PacketConn, tcpx *TcpX) {
		defer func() {
			if e := recover(); e != nil {
				Logger.Println(fmt.Sprintf("recover from panic %v", e))
			}
		}()
		var buffer []byte
		var addr net.Addr
		var e error
		for {
			// read from udp conn
			buffer, addr, e = ReadAllUDP(conn, maxBufferSize...)
			// global
			if e != nil {
				if e == io.EOF {
					break
				}
				Logger.Println(e.Error())
				break
			}
			ctx := NewUDPContext(conn, addr, tcpx.Packx.Marshaller)
			ctx.Stream, e = tcpx.Packx.FirstBlockOfBytes(buffer)
			if e != nil {
				Logger.Println(e.Error())
				break
			}
			// This function are shared among udp ListenAndServe,tcp ListenAndServe and kcp ListenAndServe.
			// But there are some important differences.
			// tcp's context is per-connection scope, some middleware offset and temporary handlers are saved in
			// this context,which means, this function can't work in parallel goroutines.But udp's context is
			// per-request scope, middleware's args are request-apart, it can work in parallel goroutines because
			// different request has different context instance.It's concurrently safe.
			// Thus we can use it like : `go func(ctx *Context, tcpx *TcpX){...}(ctx, tcpx)`
			go func(ctx *Context, tcpx *TcpX) {
				if tcpx.OnMessage != nil {
					// tcpx.Mux.execAllMiddlewares(ctx)
					//tcpx.OnMessage(ctx)
					if ctx.handlers == nil {
						ctx.handlers = make([]func(c *Context), 0, 10)
					}
					ctx.handlers = append(ctx.handlers, tcpx.Mux.GlobalMiddlewares...)
					for _, v := range tcpx.Mux.MiddlewareAnchorMap {
						ctx.handlers = append(ctx.handlers, v.Middleware)
					}
					ctx.handlers = append(ctx.handlers, tcpx.OnMessage)
					if len(ctx.handlers) > 0 {
						ctx.Next()
					}
					ctx.Reset()
				} else {
					messageID, e := tcpx.Packx.MessageIDOf(ctx.Stream)
					if e != nil {
						Logger.Println(errorx.Wrap(e).Error())
						return
					}
					handler, ok := tcpx.Mux.Handlers[messageID]
					if !ok {
						Logger.Println(fmt.Sprintf("messageID %d handler not found", messageID))
						return
					}

					//handler(ctx)

					if ctx.handlers == nil {
						ctx.handlers = make([]func(c *Context), 0, 10)
					}

					// global middleware
					ctx.handlers = append(ctx.handlers, tcpx.Mux.GlobalMiddlewares...)
					// anchor middleware
					messageIDAnchorIndex := tcpx.Mux.AnchorIndexOfMessageID(messageID)
					for _, v := range tcpx.Mux.MiddlewareAnchorMap {
						if messageIDAnchorIndex > v.AnchorIndex && messageIDAnchorIndex <= v.ExpireAnchorIndex {
							ctx.handlers = append(ctx.handlers, v.Middleware)
						}
					}
					// self-related middleware
					ctx.handlers = append(ctx.handlers, tcpx.Mux.MessageIDSelfMiddleware[messageID]...)
					// handler
					ctx.handlers = append(ctx.handlers, handler)

					if len(ctx.handlers) > 0 {
						ctx.Next()
					}
					ctx.Reset()
				}
			}(ctx, tcpx)
			continue
		}
	}(conn, tcpx)

	select {}

	return nil
}

func ReadAllUDP(conn net.PacketConn, maxBufferSize ...int) ([]byte, net.Addr, error) {
	if len(maxBufferSize) > 1 {
		panic(errorx.NewFromStringf("'tcpx.ListenAndServeUDP calls ReadAllUDP''s maxBufferSize should has length less by 1 but got %d", len(maxBufferSize)))
	}
	var buffer []byte
	if len(maxBufferSize) <= 0 {
		buffer = make([]byte, 4096, 4096)
	} else {
		buffer = make([]byte, maxBufferSize[0], maxBufferSize[0])
	}

	n, addr, e := conn.ReadFrom(buffer)
	fmt.Println(n)

	if e != nil {
		return nil, nil, e
	}
	return buffer[0:n], addr, nil
}
