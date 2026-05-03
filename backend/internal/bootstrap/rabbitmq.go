package bootstrap

import (
	"fmt"

	"github.com/rabbitmq/amqp091-go"
)

// NewRabbitMQ 创建 RabbitMQ 连接和 channel，并声明项目需要的 exchange。
func NewRabbitMQ(cfg RabbitMQConfig) (*amqp091.Connection, *amqp091.Channel, *amqp091.Channel, error) {
	// 先建立 TCP 级别的 MQ 连接。
	conn, err := amqp091.Dial(cfg.URL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	// 再从连接中打开一个 channel。
	// RabbitMQ 的很多实际操作都是通过 channel 完成的。
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("open rabbitmq channel: %w", err)
	}

	// 提前声明 exchange。
	// 这样后续发送消息时，不会因为 exchange 不存在而失败。
	if err := ch.ExchangeDeclare(
		cfg.Exchange,
		"topic",
		true,  // durable: RabbitMQ 重启后仍然保留
		false, // auto-deleted: 不自动删除
		false, // internal: 允许生产者直接发布到这个 exchange
		false, // no-wait: 等待服务器确认
		nil,
	); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("declare rabbitmq exchange: %w", err)
	}

	// 当前阶段先把项目规划里的两个核心队列都声明出来，
	// 并统一绑定到 video.* 路由键，便于后续直接接消费者。
	if err := declareAndBindQueue(ch, cfg.Exchange, cfg.QueueVideoStats, "video.*"); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, err
	}

	if err := declareAndBindQueue(ch, cfg.Exchange, cfg.QueueHotFeed, "video.*"); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, err
	}

	if err := declareAndBindQueue(ch, cfg.Exchange, cfg.QueueFollowingInbox, "video.*"); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, err
	}

	if err := declareAndBindQueue(ch, cfg.Exchange, cfg.QueueUserFollow, "user.*"); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, err
	}

	confirmCh, err := conn.Channel()
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("open rabbitmq confirm channel: %w", err)
	}
	if err := confirmCh.Confirm(false); err != nil {
		_ = confirmCh.Close()
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, nil, fmt.Errorf("enable rabbitmq publisher confirm: %w", err)
	}

	return conn, ch, confirmCh, nil
}

func declareAndBindQueue(ch *amqp091.Channel, exchange, queueName, routingKey string) error {
	if queueName == "" {
		return nil
	}

	if _, err := ch.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare rabbitmq queue %q: %w", queueName, err)
	}

	if err := ch.QueueBind(
		queueName,
		routingKey,
		exchange,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind rabbitmq queue %q: %w", queueName, err)
	}

	return nil
}
