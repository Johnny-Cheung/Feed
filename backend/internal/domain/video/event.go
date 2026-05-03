package video

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rabbitmq/amqp091-go"
)

// Publisher 负责把视频领域事件发送到 RabbitMQ。
// 在第六阶段里，它只有“发送者”这个角色，还没有实现“消费者”。
// 也就是说：
// - 现在代码只负责把“视频发布了/删除了”这件事广播出去
// - 真正去消费这些事件、更新统计和热榜，是后续阶段的工作
type Publisher struct {
	channel  *amqp091.Channel
	exchange string
}

// VideoEvent 是当前阶段发送到 MQ 的最小事件载荷。
// 结构参考规划文档中的事件载荷示例。
// 可以把它理解成一张“消息信封”：
// 告诉下游系统“发生了什么事、是哪个视频、是谁操作的、何时发生”。
type VideoEvent struct {
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	VideoID        uint64    `json:"video_id"`
	OperatorUserID uint64    `json:"operator_user_id"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func NewPublisher(channel *amqp091.Channel, exchange string) *Publisher {
	return &Publisher{
		channel:  channel,
		exchange: exchange,
	}
}

// PublishVideoPublished 发送“视频发布成功”事件。
// 当前被 Service.Publish 在主数据落库后调用。
func (p *Publisher) PublishVideoPublished(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, "video.published", videoID, operatorUserID)
}

// PublishVideoDeleted 发送“视频删除”事件。
// 当前被 Service.Delete 在软删除后调用。
func (p *Publisher) PublishVideoDeleted(ctx context.Context, videoID, operatorUserID uint64) error {
	return p.publish(ctx, "video.deleted", videoID, operatorUserID)
}

func (p *Publisher) publish(ctx context.Context, eventType string, videoID, operatorUserID uint64) error {
	// 这里先做一个最基础的运行时防御。
	// 如果启动时 MQ 没初始化成功，或者对象没正确注入，
	// 就不要继续往下发，直接返回错误。
	if p == nil || p.channel == nil {
		return fmt.Errorf("rabbitmq publisher is not initialized")
	}

	// 先把业务动作包装成统一事件对象。
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

	// 这里真正把消息交给 RabbitMQ。
	// exchange 决定“往哪类交换机发”，routing key 决定“这是什么类型的事件”。
	// 后续不同队列可以按 routing key 订阅自己关心的事件。
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
		return fmt.Errorf("publish video event: %w", err)
	}

	return nil
}

// newEventID 生成一个简单的随机事件 ID。
// 当前阶段只需要保证“足够随机且可追踪”即可。
func newEventID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UTC().UnixNano())
	}
	return "evt-" + hex.EncodeToString(buffer)
}
