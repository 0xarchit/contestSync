package config

import (
	"encoding/hex"
	"log"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL          string
	ReadDatabaseURLs     string
	ConnectionLimit      int
	ConnectionPoolLimit  int
	GoogleClientID       string
	GoogleClientSecret   string
	GoogleRedirectURL    string
	SessionSecret        []byte
	EncryptionKey        []byte
	Port                 string
	WorkerPort           string
	Env                  string
	AdminPassword        string
	AMQPURL              string
	ValkeyURI            string
	TelegramProxyURL     string
	ProxySecretKey       string
	TelegramGroupID      string
	TelegramGroupTopicID string
	From                 string
}

func Load() *Config {
	connLimit, _ := strconv.Atoi(os.Getenv("CONNECTION_LIMIT"))
	if connLimit == 0 {
		connLimit = 800
	}

	connPoolLimit, _ := strconv.Atoi(os.Getenv("CONNECTION_POOL_LIMIT"))
	if connPoolLimit == 0 {
		connPoolLimit = 10000
	}

	sessionSecret, err := hex.DecodeString(os.Getenv("SESSION_SECRET"))
	if err != nil {
		log.Fatalf("failed to decode SESSION_SECRET: %v", err)
	}
	encryptionKey, err := hex.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		log.Fatalf("failed to decode ENCRYPTION_KEY: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	workerPort := os.Getenv("WORKER_PORT")
	if workerPort == "" {
		workerPort = os.Getenv("PORT")
	}
	if workerPort == "" {
		workerPort = "8081"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	secretKey := os.Getenv("PROXY_SECRET_KEY")
	if secretKey == "" {
		secretKey = os.Getenv("ProxySecretKey")
	}

	amqpURL := os.Getenv("CLOUDAMQP_URL")
	if amqpURL == "" {
		amqpURL = os.Getenv("AMQP_URL")
	}

	return &Config{
		DatabaseURL:          os.Getenv("POSTGRES_DB"),
		ReadDatabaseURLs:     os.Getenv("POSTGRES_READ_DB"),
		ConnectionLimit:      connLimit,
		ConnectionPoolLimit:  connPoolLimit,
		GoogleClientID:       os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:   os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:    os.Getenv("GOOGLE_REDIRECT_URL"),
		SessionSecret:        sessionSecret,
		EncryptionKey:        encryptionKey,
		Port:                 port,
		WorkerPort:           workerPort,
		Env:                  env,
		AdminPassword:        os.Getenv("ADMIN_PASSWORD"),
		AMQPURL:              amqpURL,
		ValkeyURI:            os.Getenv("VALKEY_URI"),
		TelegramProxyURL:     os.Getenv("TG_PROXY_URL"),
		ProxySecretKey:       secretKey,
		TelegramGroupID:      os.Getenv("TG_GROUP_ID"),
		TelegramGroupTopicID: os.Getenv("TG_GROUP_TOPIC_ID"),
		From:                 os.Getenv("FROM"),
	}
}
