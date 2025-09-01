package main

import (
	"log"
	"os"
	"time"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	router := map[string]func(jsons []byte) ([]byte, error){
		"tes-command-1": func(jsons []byte) ([]byte, error) {
			time.Sleep(time.Second)
			return []byte("testing-1"), nil
		},
		"tes-command-2": func(jsons []byte) ([]byte, error) {
			time.Sleep(time.Second * 2)
			return []byte("testing-2"), nil
		},
		"tes-command-3": func(jsons []byte) ([]byte, error) {
			time.Sleep(time.Second * 3)
			return []byte("testing-3"), nil
		},
	}
	plugin := simplestdioplugin.NewPluginClient(router, os.Stdin, os.Stdout)
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
