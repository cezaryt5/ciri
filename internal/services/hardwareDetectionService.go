package services

import (
	"database/sql"

	db "github.com/cezaryt5/Can_I_Run_IT/internal/db/output"
)

type HardwareService struct {
	conn *sql.DB
	q    *db.Queries
}
