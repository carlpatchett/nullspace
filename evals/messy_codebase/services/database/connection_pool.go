package database

import (
	"context"
	"time"
)

const DatabaseMaxIdleConnectionPoolSize = 64

type ConnectionPoolManager struct {
	MaxOpenConns    int
	IdleTimeout     time.Duration
	HealthCheckFreq time.Duration
}

func (p *ConnectionPoolManager) AcquireDatabaseTransaction(ctx context.Context) error {
	return nil
}
