package ids

import (
	"database/sql/driver"
	"fmt"
)

// Value makes AccountID a driver.Valuer: the database sees the bare int64.
func (a AccountID) Value() (driver.Value, error) {
	return int64(a), nil
}

// Stored reports what a database driver would store for a, through the
// driver.Valuer interface.
func Stored(a AccountID) string {
	var valuer driver.Valuer = a
	v, err := valuer.Value()
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("stored %v (%T)", v, v)
}
