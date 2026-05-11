package comet

import (
	"context"
	"math/rand"
	"time"

	"github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/comet/conf"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/zhenjl/cityhash"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const (
	minServerHeartbeat = time.Minute * 10
	maxServerHeartbeat = time.Minute * 30
	// grpc options
	grpcInitialWindowSize     = 1 << 24
	grpcInitialConnWindowSize = 1 << 24
	grpcMaxSendMsgSize        = 1 << 24
	grpcMaxCallMsgSize        = 1 << 24
	grpcKeepAliveTime         = time.Second * 10
	grpcKeepAliveTimeout      = time.Second * 3
	grpcBackoffMaxDelay       = time.Second * 3
)

func newLogicClient(c *conf.RPCClient) logic.LogicClient {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Dial))
	defer cancel()
	conn, err := grpc.DialContext(ctx, "discovery://default/goim.logic",
		[]grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithInitialWindowSize(grpcInitialWindowSize),
			grpc.WithInitialConnWindowSize(grpcInitialConnWindowSize),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxCallMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(grpcMaxSendMsgSize)),
			grpc.WithBackoffMaxDelay(grpcBackoffMaxDelay),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:                grpcKeepAliveTime,
				Timeout:             grpcKeepAliveTimeout,
				PermitWithoutStream: true,
			}),
		}...)
	if err != nil {
		log.Fatalf("newLogicClient failed: %v", err)
	}
	return logic.NewLogicClient(conn)
}

// Server is comet server.
type Server struct {
	c         *conf.Config
	round     *Round    // accept round store
	buckets   []*Bucket // subkey bucket
	bucketIdx uint32
	ctx       context.Context
	cancel    context.CancelFunc

	serverID  string
	rpcClient logic.LogicClient
}

// NewServer returns a new Server.
func NewServer(c *conf.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		c:         c,
		ctx:       ctx,
		cancel:    cancel,
		round:     NewRound(c),
		rpcClient: newLogicClient(c.RPCClient),
	}
	// init bucket
	if c.Bucket.Size <= 0 {
		c.Bucket.Size = 1
	}
	s.buckets = make([]*Bucket, c.Bucket.Size)
	s.bucketIdx = uint32(c.Bucket.Size)
	for i := 0; i < c.Bucket.Size; i++ {
		s.buckets[i] = NewBucket(c.Bucket)
	}
	s.serverID = c.Env.Host
	go s.onlineproc()
	return s
}

// Buckets return all buckets.
func (s *Server) Buckets() []*Bucket {
	return s.buckets
}

// Context returns the server's context for shutdown signaling.
func (s *Server) Context() context.Context {
	return s.ctx
}

// Bucket get the bucket by subkey.
func (s *Server) Bucket(subKey string) *Bucket {
	idx := cityhash.CityHash32([]byte(subKey), uint32(len(subKey))) % s.bucketIdx
	if conf.Conf.Debug {
		log.Infof("%s hit channel bucket index: %d use cityhash", subKey, idx)
	}
	return s.buckets[idx]
}

// RandServerHearbeat rand server heartbeat.
func (s *Server) RandServerHearbeat() time.Duration {
	return (minServerHeartbeat + time.Duration(rand.Int63n(int64(maxServerHeartbeat-minServerHeartbeat))))
}

// Close close the server.
func (s *Server) Close() (err error) {
	s.cancel()
	for _, b := range s.buckets {
		b.Close()
	}
	return
}

func (s *Server) onlineproc() {
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			var (
				allRoomsCount map[string]int32
				err           error
			)
			roomCount := make(map[string]int32)
			for _, bucket := range s.buckets {
				for roomID, count := range bucket.RoomsCount() {
					roomCount[roomID] += count
				}
			}
			if allRoomsCount, err = s.RenewOnline(s.ctx, s.serverID, roomCount); err != nil {
				log.Errorf("onlineproc error(%v)", err)
				continue
			}
			for _, bucket := range s.buckets {
				bucket.UpRoomsCount(allRoomsCount)
			}
		}
	}
}
