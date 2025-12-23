package main

import (
	"math/rand"
	"os"
	"time"

	charm "github.com/charmbracelet/log"
)

var charmLevels = []charm.Level{
	charm.DebugLevel,
	charm.InfoLevel,
	charm.WarnLevel,
	charm.ErrorLevel,
	charm.FatalLevel,
}

func main() {
	charm.SetFormatter(charm.JSONFormatter)
	charm.SetOutput(os.Stdout)
	charm.SetTimeFormat(time.RFC3339)
	charm.SetReportTimestamp(true)
	for i := 0; i < 10; i++ {
		randomLog()
	}
}

func randomLog() {
	charm.Log(charmLevels[rand.Intn(len(charmLevels))], "Hello World!", "a", rand.Intn(100), "b", rand.Intn(100))
}
