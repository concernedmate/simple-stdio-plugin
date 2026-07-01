package main

import (
	"context"
	"fmt"
	"log"
	"time"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router := map[string]func(jsons []byte) ([]byte, error){
		"event": func(jsons []byte) ([]byte, error) {
			data := fmt.Sprintf("event from main.exe: %s", string(jsons))
			fmt.Println(data)
			return []byte(data), nil
		},
	}

	_, err := simplestdioplugin.StartPlugin(
		ctx, simplestdioplugin.StartPluginConfig{
			BaseDir:   "./plugin",
			Extension: "exe",
			Router:    router,
			LogFunc:   func(s string) { fmt.Println(s) },
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	time.Sleep(15 * time.Second)
}
