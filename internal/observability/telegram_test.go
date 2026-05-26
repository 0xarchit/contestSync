package observability

import (
	"context"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func TestIntegrationTelegramSend(t *testing.T) {
	godotenv.Load("../../.env")
	botToken := os.Getenv("TG_BOT_TOKEN")
	groupID := os.Getenv("TG_GROUP_ID")
	topicID := os.Getenv("TG_GROUP_TOPIC_ID")
	if botToken == "" || groupID == "" {
		t.Skip("skipping integration test; TG_BOT_TOKEN and TG_GROUP_ID not set in env")
	}
	cfg := TelegramConfig{
		BotToken: botToken,
		GroupID:  groupID,
		TopicID:  topicID,
		From:     "Go Integration Test Suite",
	}
	client := NewClient(cfg)
	ctx := context.Background()
	err := client.Send(ctx, "<b>[TEST INFO]</b> Integration test alert confirmation. All systems operational.")
	if err != nil {
		t.Fatalf("failed to send integration alert: %v", err)
	}
}
