package logic

import (
	"context"
	"strings"
	"time"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/logic/model"
	log "github.com/Terry-Mao/goim/pkg/log"
	"github.com/bilibili/discovery/naming"
)

// NodesInstances get servers info.
func (l *Logic) NodesInstances(c context.Context) (res []*naming.Instance) {
	l.nodesMu.RLock()
	res = l.nodes
	l.nodesMu.RUnlock()
	return
}

// NodesWeighted get node list.
func (l *Logic) NodesWeighted(c context.Context, platform, clientIP string) *pb.NodesReply {
	reply := &pb.NodesReply{
		Domain:       l.c.Node.DefaultDomain,
		TcpPort:      int32(l.c.Node.TCPPort),
		WsPort:       int32(l.c.Node.WSPort),
		WssPort:      int32(l.c.Node.WSSPort),
		Heartbeat:    int32(time.Duration(l.c.Node.Heartbeat) / time.Second),
		HeartbeatMax: int32(l.c.Node.HeartbeatMax),
		Backoff: &pb.Backoff{
			MaxDelay:  l.c.Backoff.MaxDelay,
			BaseDelay: l.c.Backoff.BaseDelay,
			Factor:    l.c.Backoff.Factor,
			Jitter:    l.c.Backoff.Jitter,
		},
	}
	domains, addrs := l.nodeAddrs(c, clientIP)
	if platform == model.PlatformWeb {
		reply.Nodes = domains
	} else {
		reply.Nodes = addrs
	}
	if len(reply.Nodes) == 0 {
		reply.Nodes = []string{l.c.Node.DefaultDomain}
	}
	return reply
}

func (l *Logic) nodeAddrs(c context.Context, clientIP string) (domains, addrs []string) {
	var (
		region string
	)
	province, err := l.location(c, clientIP)
	if err == nil {
		region = l.regions[province]
	}
	log.Infof("nodeAddrs clientIP:%s region:%s province:%s domains:%v addrs:%v", clientIP, region, province, domains, addrs)
	return l.loadBalancer.NodeAddrs(region, l.c.Node.HostDomain, l.c.Node.RegionWeight)
}

// location find a geolocation of an IP address including province, region and country.
func (l *Logic) location(c context.Context, clientIP string) (province string, err error) {
	if l.locationMap == nil || clientIP == "" {
		return
	}
	// Match IP prefix: try longest prefix first (e.g. "192.168.1" before "192.168")
	parts := strings.Split(clientIP, ".")
	for i := len(parts); i > 0; i-- {
		prefix := strings.Join(parts[:i], ".")
		if p, ok := l.locationMap[prefix]; ok {
			province = p
			return
		}
	}
	return
}
