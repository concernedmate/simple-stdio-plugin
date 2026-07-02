package main

import (
	"log"
	"time"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
	"github.com/google/uuid"
)

var plugin *simplestdioplugin.PluginData

func main() {
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()

		for range timer.C {
			if plugin != nil {
				_, err := plugin.Command(simplestdioplugin.MessageInput{
					Function: "event",
					Data:     []byte(uuid.New().String()),
				})
				if err != nil {
					log.Fatal(err)
				}
			}
			timer.Reset(time.Second)
		}
	}()
	plugin = simplestdioplugin.NewPluginClient(simplestdioplugin.PluginClientConfig{})
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
