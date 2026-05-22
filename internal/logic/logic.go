package logic

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Terry-Mao/goim/api/protocol"
	routerpb "github.com/Terry-Mao/goim/api/router"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	"github.com/Terry-Mao/goim/internal/logic/dao"
	"github.com/Terry-Mao/goim/internal/logic/model"
	"github.com/Terry-Mao/goim/internal/logic/service"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/Terry-Mao/goim/pkg/snowflake"
	"github.com/bilibili/discovery/naming"
)

const (
	_onlineTick     = time.Second * 10
	_onlineDeadline = time.Minute * 5
)

// Logic struct
type Logic struct {
	c      *conf.Config
	dis    *naming.Discovery
	dao    *dao.Dao
	ctx    context.Context
	cancel context.CancelFunc
	// online (protected by nodesMu)
	nodesMu    sync.RWMutex
	totalIPs   int64
	totalConns int64
	roomCount  map[string]int32
	// load balancer
	nodes        []*naming.Instance
	loadBalancer *LoadBalancer
	regions      map[string]string // province -> region
	locationMap  map[string]string // IP prefix -> province
	// services
	sessionMgr   *service.SessionManager
	onlineRouter *service.OnlineRouter
	syncSvc      *service.SyncService
	cometPusher  *CometPusher
	routerClient routerpb.RouterClient
	routerPool   *RouterClientPool
	idGen        *snowflake.Snowflake
}

// New init
func New(c *conf.Config) (l *Logic) {
	ctx, cancel := context.WithCancel(context.Background())
	l = &Logic{
		c:            c,
		ctx:          ctx,
		cancel:       cancel,
		dao:          dao.New(c),
		dis:          naming.New(c.Discovery),
		loadBalancer: NewLoadBalancer(),
		regions:      make(map[string]string),
	}
	// Initialize snowflake ID generator
	if c.Snowflake != nil {
		if idGen, err := snowflake.New(c.Snowflake.MachineID); err == nil {
			l.idGen = idGen
		} else {
			panic(err)
		}
	} else {
		idGen, err := snowflake.New(0)
		if err != nil {
			panic(err)
		}
		l.idGen = idGen
	}
	// Initialize services
	sessionTTL := time.Duration(c.Redis.Expire)
	l.onlineRouter = service.NewOnlineRouter()
	l.sessionMgr = service.NewSessionManager(l.dao, sessionTTL)
	l.sessionMgr.SetOnlineRouter(l.onlineRouter)
	l.cometPusher = NewCometPusher()
	l.sessionMgr.SetKicker(l.cometPusher)
	l.routerPool = NewRouterClientPool()
	l.routerClient = l.routerPool
	l.syncSvc = service.NewSyncService(l.dao, l.sessionMgr, service.NewRouterDirectPusher(l.routerClient))

	l.initRegions()
	l.initNodes()
	l.initRouterNodes()
	_ = l.loadOnline()
	go l.onlineproc()
	return l
}

// Ping ping resources is ok.
func (l *Logic) Ping(c context.Context) (err error) {
	return l.dao.Ping(c)
}

// Close close resources.
func (l *Logic) Close() {
	l.cancel()
	if l.routerPool != nil {
		l.routerPool.Close()
	}
	l.cometPusher.Close()
	l.dao.Close()
}

func (l *Logic) initRegions() {
	for region, ps := range l.c.Regions {
		for _, province := range ps {
			l.regions[province] = region
		}
	}
	// Load IP prefix -> province mapping from config
	if l.c.Location != nil {
		l.locationMap = l.c.Location
	}
}

func (l *Logic) initNodes() {
	res := l.dis.Build("goim.comet")
	event := res.Watch()
	select {
	case _, ok := <-event:
		if ok {
			l.newNodes(res)
		} else {
			panic("discovery watch failed")
		}
	case <-time.After(10 * time.Second):
		log.Error("discovery start timeout")
	}
	go func() {
		for {
			select {
			case <-l.ctx.Done():
				return
			case ev, ok := <-event:
				if !ok {
					return
				}
				_ = ev
				l.newNodes(res)
			}
		}
	}()
}

func (l *Logic) initRouterNodes() {
	res := l.dis.Build("goim.router")
	event := res.Watch()
	select {
	case _, ok := <-event:
		if ok {
			l.newRouterNodes(res)
		} else {
			log.Error("router discovery watch failed")
		}
	case <-time.After(10 * time.Second):
		log.Error("router discovery start timeout")
	}
	go func() {
		for {
			select {
			case <-l.ctx.Done():
				return
			case _, ok := <-event:
				if !ok {
					return
				}
				l.newRouterNodes(res)
			}
		}
	}()
}

func (l *Logic) newNodes(res naming.Resolver) {
	if zoneIns, ok := res.Fetch(); ok {
		var (
			totalConns int64
			totalIPs   int64
			allIns     []*naming.Instance
		)
		for _, zins := range zoneIns.Instances {
			for _, ins := range zins {
				if ins.Metadata == nil {
					log.Errorf("node instance metadata is empty(%+v)", ins)
					continue
				}
				offline, err := strconv.ParseBool(ins.Metadata[model.MetaOffline])
				if err != nil || offline {
					log.Warningf("strconv.ParseBool(offline:%t) error(%v)", offline, err)
					continue
				}
				conns, err := strconv.ParseInt(ins.Metadata[model.MetaConnCount], 10, 32)
				if err != nil {
					log.Errorf("strconv.ParseInt(conns:%d) error(%v)", conns, err)
					continue
				}
				ips, err := strconv.ParseInt(ins.Metadata[model.MetaIPCount], 10, 32)
				if err != nil {
					log.Errorf("strconv.ParseInt(ips:%d) error(%v)", ips, err)
					continue
				}
				totalConns += conns
				totalIPs += ips
				allIns = append(allIns, ins)
			}
		}
		l.nodesMu.Lock()
		l.totalConns = totalConns
		l.totalIPs = totalIPs
		l.nodes = allIns
		l.nodesMu.Unlock()
		l.loadBalancer.Update(allIns)
		l.cometPusher.UpdateNodes(allIns)
	}
}

func (l *Logic) newRouterNodes(res naming.Resolver) {
	if l.routerPool == nil {
		return
	}
	if zoneIns, ok := res.Fetch(); ok {
		var allIns []*naming.Instance
		for _, zins := range zoneIns.Instances {
			for _, ins := range zins {
				allIns = append(allIns, ins)
			}
		}
		l.routerPool.UpdateNodes(allIns)
	}
}

func (l *Logic) onlineproc() {
	ticker := time.NewTicker(_onlineTick)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			if err := l.loadOnline(); err != nil {
				log.Errorf("onlineproc error(%v)", err)
			}
		}
	}
}

// PushToUser pushes a message to a specific user via the Message Router (Phase 2).
func (l *Logic) PushToUser(c context.Context, msgID string, toUID int64, op int32, body []byte, seq int64) error {
	reply, err := l.routerClient.RouteByUser(c, &routerpb.RouteByUserReq{
		MsgId: msgID,
		ToUid: toUID,
		Op:    op,
		Body:  body,
		Seq:   seq,
	})
	if err != nil {
		return err
	}
	if reply != nil && reply.ErrorCode != "" {
		return fmt.Errorf("router route by user failed: %s: %s", reply.ErrorCode, reply.ErrorMessage)
	}
	return nil
}

// AckMessage handles a message ACK from a client.
func (l *Logic) AckMessage(c context.Context, mid int64, msgID string) error {
	_, err := l.routerClient.HandleACK(c, &routerpb.HandleACKReq{Uid: mid, MsgId: msgID})
	return err
}

// GetOfflineMessages returns offline messages for a user since lastSeq.
func (l *Logic) GetOfflineMessages(c context.Context, mid int64, lastSeq int64, limit int32) (*protocol.SyncReplyBody, error) {
	return l.syncSvc.GetOfflineMessages(c, mid, lastSeq, limit)
}

// GenerateMsgID generates a unique message ID using snowflake.
func (l *Logic) GenerateMsgID() string {
	if l.idGen != nil {
		if id, err := l.idGen.GenerateString(); err == nil {
			return id
		} else {
			log.Errorf("snowflake generate message id failed: %v", err)
		}
	}
	return ""
}

func (l *Logic) loadOnline() (err error) {
	var (
		roomCount = make(map[string]int32)
	)
	l.nodesMu.RLock()
	nodes := l.nodes
	l.nodesMu.RUnlock()
	for _, server := range nodes {
		var online *model.Online
		online, err = l.dao.ServerOnline(context.Background(), server.Hostname)
		if err != nil {
			return
		}
		if time.Since(time.Unix(online.Updated, 0)) > _onlineDeadline {
			_ = l.dao.DelServerOnline(context.Background(), server.Hostname)
			continue
		}
		for roomID, count := range online.RoomCount {
			roomCount[roomID] += count
		}
	}
	l.nodesMu.Lock()
	l.roomCount = roomCount
	l.nodesMu.Unlock()
	return
}
