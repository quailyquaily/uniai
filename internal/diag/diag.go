package diag

import (
	"encoding/json"
	"log"
)

func LogJSON(enabled bool, label string, value any) {
	if !enabled {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("%s: <marshal error: %v>", label, err)
		return
	}
	log.Printf("%s: %s", label, string(data))
}

func LogText(enabled bool, label string, text string) {
	if !enabled {
		return
	}
	log.Printf("%s: %s", label, text)
}
