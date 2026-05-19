package config

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL     string
	CACertificate   []byte
	ConnectionLimit int
	GoogleClientID  string
	GoogleClientSecret string
	GoogleRedirectURL string
	SessionSecret   []byte
	CSRFSecret      []byte
	EncryptionKey   []byte
	Port            string
	Env             string
	AllowedOrigin   string
	AdminPassword   string
	KafkaHost       string
	KafkaPort       string
	KafkaAccessKey  []byte
	KafkaAccessCert []byte
	KafkaCACert     []byte
}

func Load() *Config {
	caCertBase64 := os.Getenv("CA_CERTIFICATE")
	var caCert []byte
	if caCertBase64 != "" {
		caCert, _ = base64.StdEncoding.DecodeString(caCertBase64)
	}

	kafkaAccessKey, _ := base64.StdEncoding.DecodeString(os.Getenv("KAFKA_ACCESS_KEY"))
	kafkaAccessCert, _ := base64.StdEncoding.DecodeString(os.Getenv("KAFKA_ACCESS_CERTIFICATE"))
	kafkaCACert, _ := base64.StdEncoding.DecodeString(os.Getenv("KAFKA_CA_CERTIFICATE"))

	connLimit, _ := strconv.Atoi(os.Getenv("CONNECTION_LIMIT"))
	if connLimit == 0 {
		connLimit = 10
	}

	sessionSecret, _ := hex.DecodeString(os.Getenv("SESSION_SECRET"))
	csrfSecret, _ := hex.DecodeString(os.Getenv("CSRF_SECRET"))
	encryptionKey, _ := hex.DecodeString(os.Getenv("ENCRYPTION_KEY"))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	return &Config{
		DatabaseURL:     os.Getenv("POSTGRES_DB"),
		CACertificate:   caCert,
		ConnectionLimit: connLimit,
		GoogleClientID:  os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL: os.Getenv("GOOGLE_REDIRECT_URL"),
		SessionSecret:   sessionSecret,
		CSRFSecret:      csrfSecret,
		EncryptionKey:   encryptionKey,
		Port:            port,
		Env:             env,
		AllowedOrigin:   os.Getenv("ALLOWED_ORIGIN"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		KafkaHost:       os.Getenv("KAFKA_HOST"),
		KafkaPort:       os.Getenv("KAFKA_PORT"),
		KafkaAccessKey:  kafkaAccessKey,
		KafkaAccessCert: kafkaAccessCert,
		KafkaCACert:     kafkaCACert,
	}
}
