package config

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// PostgreSQL 连接配置
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// GetDefaultPostgresConfig 返回默认配置
func GetDefaultPostgresConfig() *PostgresConfig {
	return &PostgresConfig{
		Host:     getEnv("PG_HOST", "localhost"),
		Port:     5432,
		User:     getEnv("PG_USER", "gexue"),
		Password: getEnv("PG_PASSWORD", "gexue_password"),
		DBName:   getEnv("PG_DBNAME", "gexue"),
		SSLMode:  getEnv("PG_SSLMODE", "disable"),
	}
}

// DSN 生成 PostgreSQL 连接字符串
func (pc *PostgresConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		pc.Host,
		pc.Port,
		pc.User,
		pc.Password,
		pc.DBName,
		pc.SSLMode,
	)
}

// ConnectPostgres 连接 PostgreSQL 数据库
func ConnectPostgres(cfg *PostgresConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}
	return db, nil
}

// getEnv 获取环境变量，提供默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
