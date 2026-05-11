package dao

import (
	"context"
	"testing"

	pb "github.com/Terry-Mao/goim/api/logic"
	"github.com/Terry-Mao/goim/internal/logic/conf"
	"github.com/stretchr/testify/assert"
)

func TestDaoPushMsg(t *testing.T) {
	var (
		c      = context.Background()
		op     = int32(100)
		server = "test"
		msg    = []byte("msg")
		keys   = []string{"key"}
	)
	err := d.PushMsg(c, op, server, keys, msg)
	assert.Nil(t, err)
}

func TestDaoBroadcastRoomMsg(t *testing.T) {
	var (
		c    = context.Background()
		op   = int32(100)
		room = "test://1"
		msg  = []byte("msg")
	)
	err := d.BroadcastRoomMsg(c, op, room, msg)
	assert.Nil(t, err)
}

func TestDaoBroadcastMsg(t *testing.T) {
	var (
		c     = context.Background()
		op    = int32(100)
		speed = int32(0)
		msg   = []byte("")
	)
	err := d.BroadcastMsg(c, op, speed, msg)
	assert.Nil(t, err)
}

func TestTopicFor(t *testing.T) {
	// Test with split topics configured
	dSplit := &Dao{
		c: &conf.Config{
			Kafka: &conf.Kafka{
				Topic:     "legacy_topic",
				PushTopic: "push_topic",
				RoomTopic: "room_topic",
				AllTopic:  "all_topic",
			},
		},
	}
	assert.Equal(t, "push_topic", dSplit.topicFor(pb.PushMsg_PUSH))
	assert.Equal(t, "room_topic", dSplit.topicFor(pb.PushMsg_ROOM))
	assert.Equal(t, "all_topic", dSplit.topicFor(pb.PushMsg_BROADCAST))

	// Test fallback to legacy topic when split topics are empty
	dLegacy := &Dao{
		c: &conf.Config{
			Kafka: &conf.Kafka{
				Topic: "legacy_topic",
			},
		},
	}
	assert.Equal(t, "legacy_topic", dLegacy.topicFor(pb.PushMsg_PUSH))
	assert.Equal(t, "legacy_topic", dLegacy.topicFor(pb.PushMsg_ROOM))
	assert.Equal(t, "legacy_topic", dLegacy.topicFor(pb.PushMsg_BROADCAST))

	// Test partial split: only PushTopic configured
	dPartial := &Dao{
		c: &conf.Config{
			Kafka: &conf.Kafka{
				Topic:     "legacy_topic",
				PushTopic: "push_topic",
			},
		},
	}
	assert.Equal(t, "push_topic", dPartial.topicFor(pb.PushMsg_PUSH))
	assert.Equal(t, "legacy_topic", dPartial.topicFor(pb.PushMsg_ROOM))
	assert.Equal(t, "legacy_topic", dPartial.topicFor(pb.PushMsg_BROADCAST))
}
