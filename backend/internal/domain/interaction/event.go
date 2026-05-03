package interaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

const reliablePublishConfirmTimeout = time.Second

const (
	EventTypeVideoPublished      = "video.published"
	EventTypeVideoDeleted        = "video.deleted"
	EventTypeVideoLiked          = "video.liked"
	EventTypeVideoUnliked        = "video.unliked"
	EventTypeVideoFavorited      = "video.favorited"
	EventTypeVideoUnfavorited    = "video.unfavorited"
	EventTypeVideoCommented      = "video.commented"
	EventTypeVideoCommentDeleted = "video.comment_deleted"
	EventTypeUserFollowed        = "user.followed"
	EventTypeUserUnfollowed      = "user.unfollowed"
)

type VideoEvent struct {
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	VideoID        uint64    `json:"video_id"`
	OperatorUserID uint64    `json:"operator_user_id"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type FollowEvent struct {
	EventID      string    `json:"event_id"`
	EventType    string    `json:"event_type"`
	UserID       uint64    `json:"user_id"`
	TargetUserID uint64    `json:"target_user_id"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type Publisher struct {
	channel                *amqp091.Channel
	reliableConfirmChannel *amqp091.Channel
	reliableConfirmations  <-chan amqp091.Confirmation
	exchange               string
	reliableConfirmMu      sync.Mutex
	nextReliableConfirmTag uint64
}

func NewPublisher(channel *amqp091.Channel, reliableConfirmChannel *amqp091.Channel, exchange string) *Publisher {
	var reliableConfirmations <-chan amqp091.Confirmation
	if reliableConfirmChannel != nil {
		reliableConfirmations = reliableConfirmChannel.NotifyPublish(make(chan amqp091.Confirmation, 64))
	}

	return &Publisher{
		channel:                channel,
		reliableConfirmChannel: reliableConfirmChannel,
		reliableConfirmations:  reliableConfirmations,
		exchange:               exchange,
		nextReliableConfirmTag: 1,
	}
}

func (p *Publisher) PublishVideoLiked(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, EventTypeVideoLiked, videoID, operatorUserID)
}

func (p *Publisher) PublishVideoUnliked(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, EventTypeVideoUnliked, videoID, operatorUserID)
}

func (p *Publisher) PublishVideoFavorited(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, EventTypeVideoFavorited, videoID, operatorUserID)
}

func (p *Publisher) PublishVideoUnfavorited(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, EventTypeVideoUnfavorited, videoID, operatorUserID)
}

func (p *Publisher) PublishVideoCommented(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publishVideoWithConfirm(ctx, EventTypeVideoCommented, videoID, operatorUserID)
}

func (p *Publisher) PublishVideoCommentDeleted(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publishVideoWithConfirm(ctx, EventTypeVideoCommentDeleted, videoID, operatorUserID)
}

func (p *Publisher) PublishUserFollowed(ctx context.Context, userID, targetUserID uint64) error {
	return p.publishFollowWithConfirm(ctx, EventTypeUserFollowed, userID, targetUserID)
}

func (p *Publisher) PublishUserUnfollowed(ctx context.Context, userID, targetUserID uint64) error {
	return p.publishFollowWithConfirm(ctx, EventTypeUserUnfollowed, userID, targetUserID)
}

func (p *Publisher) publish(ctx context.Context, eventType string, videoID, operatorUserID uint64) error {
	if p == nil || p.channel == nil {
		return fmt.Errorf("rabbitmq publisher is not initialized")
	}

	event := VideoEvent{
		EventID:        newEventID(),
		EventType:      eventType,
		VideoID:        videoID,
		OperatorUserID: operatorUserID,
		OccurredAt:     time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal interaction event: %w", err)
	}

	if err := p.channel.PublishWithContext(
		ctx,
		p.exchange,
		eventType,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			Body:         body,
			Timestamp:    event.OccurredAt,
		},
	); err != nil {
		return fmt.Errorf("publish interaction event: %w", err)
	}

	return nil
}

func (p *Publisher) publishFollow(ctx context.Context, eventType string, userID, targetUserID uint64) error {
	if p == nil || p.channel == nil {
		return fmt.Errorf("rabbitmq publisher is not initialized")
	}

	event := FollowEvent{
		EventID:      newEventID(),
		EventType:    eventType,
		UserID:       userID,
		TargetUserID: targetUserID,
		OccurredAt:   time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal follow event: %w", err)
	}

	if err := p.channel.PublishWithContext(
		ctx,
		p.exchange,
		eventType,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			Body:         body,
			Timestamp:    event.OccurredAt,
		},
	); err != nil {
		return fmt.Errorf("publish follow event: %w", err)
	}

	return nil
}

func (p *Publisher) publishVideoWithConfirm(ctx context.Context, eventType string, videoID, operatorUserID uint64) error {
	if p == nil || p.reliableConfirmChannel == nil || p.reliableConfirmations == nil {
		return fmt.Errorf("rabbitmq confirm publisher is not initialized")
	}

	event := VideoEvent{
		EventID:        newEventID(),
		EventType:      eventType,
		VideoID:        videoID,
		OperatorUserID: operatorUserID,
		OccurredAt:     time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal video event: %w", err)
	}

	return p.publishWithConfirm(ctx, eventType, body, event.OccurredAt)
}

func (p *Publisher) publishFollowWithConfirm(ctx context.Context, eventType string, userID, targetUserID uint64) error {
	if p == nil || p.reliableConfirmChannel == nil || p.reliableConfirmations == nil {
		return fmt.Errorf("rabbitmq confirm publisher is not initialized")
	}

	event := FollowEvent{
		EventID:      newEventID(),
		EventType:    eventType,
		UserID:       userID,
		TargetUserID: targetUserID,
		OccurredAt:   time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal follow event: %w", err)
	}

	return p.publishWithConfirm(ctx, eventType, body, event.OccurredAt)
}

func (p *Publisher) publishWithConfirm(ctx context.Context, eventType string, body []byte, occurredAt time.Time) error {
	p.reliableConfirmMu.Lock()
	defer p.reliableConfirmMu.Unlock()

	if err := p.reliableConfirmChannel.PublishWithContext(
		ctx,
		p.exchange,
		eventType,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			Body:         body,
			Timestamp:    occurredAt,
		},
	); err != nil {
		return fmt.Errorf("publish reliable event: %w", err)
	}

	expectedTag := p.nextReliableConfirmTag
	p.nextReliableConfirmTag++
	if err := p.waitReliablePublishConfirm(ctx, expectedTag); err != nil {
		return fmt.Errorf("confirm reliable event publish: %w", err)
	}
	return nil
}

func (p *Publisher) waitReliablePublishConfirm(ctx context.Context, expectedTag uint64) error {
	waitCtx, cancel := context.WithTimeout(ctx, reliablePublishConfirmTimeout)
	defer cancel()

	for {
		select {
		case confirm, ok := <-p.reliableConfirmations:
			if !ok {
				return fmt.Errorf("rabbitmq confirm channel closed")
			}
			if confirm.DeliveryTag < expectedTag {
				continue
			}
			if confirm.DeliveryTag > expectedTag {
				return fmt.Errorf("unexpected rabbitmq confirm tag: expected=%d got=%d", expectedTag, confirm.DeliveryTag)
			}
			if !confirm.Ack {
				return fmt.Errorf("rabbitmq nack for delivery tag %d", confirm.DeliveryTag)
			}
			return nil
		case <-waitCtx.Done():
			return waitCtx.Err()
		}
	}
}

func newEventID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UTC().UnixNano())
	}
	return "evt-" + hex.EncodeToString(buffer)
}
