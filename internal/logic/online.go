package logic

import (
	"context"
	"sort"
	"strings"

	"github.com/Terry-Mao/goim/internal/logic/model"
	log "github.com/Terry-Mao/goim/pkg/log"
)

var (
	_emptyTops = make([]*model.Top, 0)
)

// OnlineSummary contains the aggregate online and backlog metrics used by
// operational dashboards.
type OnlineSummary struct {
	IPCount        int64 `json:"ip_count"`
	ConnCount      int64 `json:"conn_count"`
	UserCount      int64 `json:"user_count"`
	OfflinePending int64 `json:"offline_pending"`
	DirectPushed   int64 `json:"direct_pushed"`
	KafkaFallback  int64 `json:"kafka_fallback"`
}

// OnlineTop get the top online.
func (l *Logic) OnlineTop(c context.Context, typ string, n int) (tops []*model.Top, err error) {
	l.nodesMu.RLock()
	defer l.nodesMu.RUnlock()
	for key, cnt := range l.roomCount {
		if strings.HasPrefix(key, typ) {
			_, roomID, err := model.DecodeRoomKey(key)
			if err != nil {
				continue
			}
			top := &model.Top{
				RoomID: roomID,
				Count:  cnt,
			}
			tops = append(tops, top)
		}
	}
	sort.Slice(tops, func(i, j int) bool {
		return tops[i].Count > tops[j].Count
	})
	if len(tops) > n {
		tops = tops[:n]
	}
	if len(tops) == 0 {
		tops = _emptyTops
	}
	return
}

// OnlineRoom get rooms online.
func (l *Logic) OnlineRoom(c context.Context, typ string, rooms []string) (res map[string]int32, err error) {
	l.nodesMu.RLock()
	defer l.nodesMu.RUnlock()
	res = make(map[string]int32, len(rooms))
	for _, room := range rooms {
		res[room] = l.roomCount[model.EncodeRoomKey(typ, room)]
	}
	return
}

// OnlineTotal get all online.
func (l *Logic) OnlineTotal(c context.Context) (int64, int64) {
	l.nodesMu.RLock()
	defer l.nodesMu.RUnlock()
	return l.totalIPs, l.totalConns
}

// OnlineSummary returns connection counts from service discovery plus session
// and offline queue totals from Redis.
func (l *Logic) OnlineSummary(c context.Context) OnlineSummary {
	ips, conns := l.OnlineTotal(c)
	delivery := l.router.Stats()
	summary := OnlineSummary{
		IPCount:       ips,
		ConnCount:     conns,
		DirectPushed:  delivery.Direct,
		KafkaFallback: delivery.Kafka,
	}

	userCount, err := l.dao.CountActiveUsers(c)
	if err != nil {
		log.Warningf("count active users failed: %v", err)
	} else {
		summary.UserCount = userCount
	}

	offlinePending, err := l.dao.CountOfflinePending(c)
	if err != nil {
		log.Warningf("count offline pending failed: %v", err)
	} else {
		summary.OfflinePending = offlinePending
	}
	return summary
}
