package taskstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func NewTaskID() string {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("tsk-%x-%s", time.Now().Unix(), hex.EncodeToString(rnd[:]))
}
