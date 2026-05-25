package config

import (
	"encoding/hex"
	"log"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL        string
	CACertificate      []byte
	ConnectionLimit    int
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	SessionSecret      []byte
	EncryptionKey      []byte
	Port               string
	WorkerPort         string
	Env                string
	AllowedOrigin      string
	AdminPassword      string
	KafkaHost          string
	KafkaPort          string
	KafkaAccessKey     []byte
	KafkaAccessCert    []byte
	KafkaCACert            []byte
	KafkaPartitions        int
	KafkaReplicationFactor int
	ValkeyURI              string
}

func Load() *Config {
	var caCert []byte
	if raw := os.Getenv("CA_CERTIFICATE"); raw != "" {
		caCert = []byte(raw)
	}

	kafkaAccessKey := []byte(os.Getenv("KAFKA_ACCESS_KEY"))
	kafkaAccessCert := []byte(os.Getenv("KAFKA_ACCESS_CERTIFICATE"))
	kafkaCACert := []byte(os.Getenv("KAFKA_CA_CERTIFICATE"))

	kafkaPartitions, err := strconv.Atoi(os.Getenv("KAFKA_PARTITIONS"))
	if err != nil || kafkaPartitions <= 0 {
		kafkaPartitions = 4
	}

	kafkaRepFactor, err := strconv.Atoi(os.Getenv("KAFKA_REPLICATION_FACTOR"))
	if err != nil || kafkaRepFactor <= 0 {
		kafkaRepFactor = 1
	}

	connLimit, _ := strconv.Atoi(os.Getenv("CONNECTION_LIMIT"))
	if connLimit == 0 {
		connLimit = 10
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
		workerPort = "8081"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	return &Config{
		DatabaseURL:        os.Getenv("POSTGRES_DB"),
		CACertificate:      caCert,
		ConnectionLimit:    connLimit,
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"),
		SessionSecret:      sessionSecret,
		EncryptionKey:      encryptionKey,
		Port:               port,
		WorkerPort:         workerPort,
		Env:                env,
		AllowedOrigin:      os.Getenv("ALLOWED_ORIGIN"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		KafkaHost:          os.Getenv("KAFKA_HOST"),
		KafkaPort:          os.Getenv("KAFKA_PORT"),
		KafkaAccessKey:     kafkaAccessKey,
		KafkaAccessCert:    kafkaAccessCert,
		KafkaCACert:            kafkaCACert,
		KafkaPartitions:        kafkaPartitions,
		KafkaReplicationFactor: kafkaRepFactor,
		ValkeyURI:              os.Getenv("VALKEY_URI"),
	}
}
