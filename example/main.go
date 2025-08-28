package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	ctx := context.Background()
	mapped := simplestdioplugin.PluginMap{Map: &sync.Map{}}

	go func() {
		if err := simplestdioplugin.PluginRunner(ctx, &mapped, "./plugins", "exe"); err != nil {
			log.Fatal(err)
		}
	}()

	for {
		plugins, err := mapped.GetPluginList()
		if err != nil {
			log.Fatal(err)
		}

		if len(plugins) > 0 {
			result, err := plugins[0].Command([]byte("tes-command"))
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println("result: ", string(result))
		}

		time.Sleep(time.Second)
	}
}
