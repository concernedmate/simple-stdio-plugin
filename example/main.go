package main

import (
	"fmt"
	"log"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

func main() {
	mapped := simplestdioplugin.NewPluginMap()
	go func() {
		if err := simplestdioplugin.PluginRunner(&mapped, "./plugins", "exe"); err != nil {
			log.Fatal(err)
		}
	}()

	for {
		plugins, err := mapped.GetPluginList()
		if err != nil {
			log.Fatal(err)
		}

		if len(plugins) > 0 {
			name := plugins[0].Name

			go func() {
				for {
					plugin, err := mapped.GetPluginByName(name)
					if err != nil {
						fmt.Println(err)
						continue
					}

					fmt.Println(name, ": tes-command-1")
					result, err := plugin.Command([]byte("tes-command-1"))
					if err != nil {
						fmt.Println(err)
						continue
					}
					fmt.Println(name, ":", string(result))
				}
			}()
			go func() {
				for {
					plugin, err := mapped.GetPluginByName(name)
					if err != nil {
						fmt.Println(err)
						continue
					}

					fmt.Println(name, ": tes-command-2")
					result, err := plugin.Command([]byte("tes-command-2"))
					if err != nil {
						fmt.Println(err)
						continue
					}
					fmt.Println(name, ":", string(result))
				}
			}()
			go func() {
				for {
					plugin, err := mapped.GetPluginByName(name)
					if err != nil {
						fmt.Println(err)
						continue
					}

					fmt.Println(name, ": tes-command-3")
					result, err := plugin.Command([]byte("tes-command-3"))
					if err != nil {
						fmt.Println(err)
						continue
					}
					fmt.Println(name, ":", string(result))
				}
			}()

			break
		}
	}
	select {}
}
