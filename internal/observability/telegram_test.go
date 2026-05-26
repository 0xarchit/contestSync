package observability

import (
	"context"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func TestIntegrationTelegramSend(t *testing.T) {
	godotenv.Load("../../.env")
	proxyURL := os.Getenv("TG_PROXY_URL")
	secretKey := os.Getenv("PROXY_SECRET_KEY")
	if secretKey == "" {
		secretKey = os.Getenv("ProxySecretKey")
	}
	groupID := os.Getenv("TG_GROUP_ID")
	topicID := os.Getenv("TG_GROUP_TOPIC_ID")
	if proxyURL == "" || groupID == "" {
		t.Skip("skipping integration test; TG_PROXY_URL and TG_GROUP_ID not set in env")
	}
	cfg := TelegramConfig{
		ProxyURL:  proxyURL,
		SecretKey: secretKey,
		GroupID:   groupID,
		TopicID:   topicID,
		From:      "Go Integration Test Suite",
	}
	client := NewClient(cfg)
	ctx := context.Background()
	err := client.Send(ctx, "<b>[TEST INFO]</b> Integration test alert confirmation. All systems operational.")
	if err != nil {
		t.Fatalf("failed to send integration alert: %v", err)
	}
}
