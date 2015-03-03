package main

import (
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/pressly/prefixer"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v2"
)

type Command struct {
	Name   string `yaml:-`
	Desc   string `yaml:"desc"`
	Exec   string `yaml:"exec`
	Script string `yaml:"script"`
}

// Config represents the configuration data that are
// loaded from the Supfile YAML file.
type Config struct {
	Hosts    map[string][]string `yaml:"hosts"`
	Env      map[string]string   `yaml:"env"`
	Commands map[string]Command  `yaml:"commands"`
	Targets  map[string][]string `yaml:"targets"`
}

func usage(conf *Config) {
	switch len(os.Args) {
	case 2:
		log.Println("Usage: sup <hosts> <target/command>\n")
		fallthrough
	case 3:
		log.Println("Available targets (from Supfile):")
		for target, _ := range conf.Targets {
			log.Printf("- %v\n", target)
		}
		log.Println("Available commands (from Supfile):")
		for cmd, _ := range conf.Commands {
			log.Printf("- %v\n", cmd)
		}
	case 1:
		log.Println("Usage: sup <hosts> <target/command>")
		log.Println("Available hosts (from Supfile):")
		for group, hosts := range conf.Hosts {
			log.Printf("- %v\n", group)
			for _, host := range hosts {
				log.Printf("   - %v\n", host)
			}
		}
	}
	os.Exit(1)
}

func main() {
	var (
		conf       Config
		commands   []Command
		paddingLen int
	)

	data, _ := ioutil.ReadFile("./Supfile")
	if err := yaml.Unmarshal(data, &conf); err != nil {
		log.Fatal(err)
	}

	if len(os.Args) != 3 {
		usage(&conf)
	}

	hosts, ok := conf.Hosts[os.Args[1]]
	if !ok || len(hosts) == 0 {
		usage(&conf)
	}

	target, isTarget := conf.Targets[os.Args[2]]
	if isTarget {
		for _, cmd := range target {
			command, isCommand := conf.Commands[cmd]
			if !isCommand {
				log.Printf("Unknown command \"%v\" (from target \"%v\": %v)\n\n", cmd, os.Args[2], target)
				usage(&conf)
			}
			command.Name = cmd
			commands = append(commands, command)
		}
	} else {
		command, isCommand := conf.Commands[os.Args[2]]
		if !isCommand {
			// Not a target, nor command
			log.Printf("Unknown target/command \"%v\"\n\n", os.Args[2])
			usage(&conf)
		}
		command.Name = os.Args[2]
		commands = append(commands, command)
	}

	env := ``
	for name, value := range conf.Env {
		env += `export ` + name + `="` + value + `";`
	}

	clients := make([]*SSHClient, len(hosts))
	for i, host := range hosts {
		c := &SSHClient{
			Env: env,
		}
		if err := c.Connect(host); err != nil {
			log.Fatal(err)
		}
		defer c.Close()

		len := len(c.User) + 1 + len(c.Host)
		if len > paddingLen {
			paddingLen = len
		}

		clients[i] = c
	}

	for _, cmd := range commands {
		if cmd.Exec != "" {
			log.Printf("Run command \"%v\": Exec \"%v\"", cmd.Name, cmd.Exec)
		} else if cmd.Script != "" {
			log.Printf("Run command \"%v\": Exec script \"%v\"", cmd.Name, cmd.Script)
			f, err := os.Open(cmd.Script)
			if err != nil {
				log.Fatal(err)
			}
			data, err = ioutil.ReadAll(f)
			if err != nil {
				log.Fatal(err)
			}
			cmd.Exec = string(data)
		} else {
			log.Fatalf("Run command \"%v\": Nothing to run", cmd.Name)
		}

		for _, c := range clients {
			padding := strings.Repeat(" ", paddingLen-(len(c.User)+1+len(c.Host)))
			c.Prefix = padding + c.User + "@" + c.Host + " | "
			c.Run(cmd)

			go func(c *SSHClient) {
				if _, err := io.Copy(os.Stdout, prefixer.New(c.RemoteStdout, c.Prefix)); err != nil {
					log.Printf("%sSTDOUT error: %v", c.Prefix, c.Host, err)
				}
			}(c)
			go func(c *SSHClient) {
				if _, err := io.Copy(os.Stderr, prefixer.New(c.RemoteStderr, c.Prefix)); err != nil {
					log.Printf("%sSTDERR error: %v", c.Prefix, c.Host, err)
				}
			}(c)
		}

		for _, c := range clients {
			if err := c.Wait(); err != nil {
				//TODO: Handle the SSH ExitError in ssh.go
				e, ok := err.(*ssh.ExitError)
				if !ok {
					log.Fatalf("%sexpected *ExitError but got %T", c.Prefix, err)
				}
				if e.ExitStatus() != 15 {
					log.Fatalf("%sexit %v", c.Prefix, e.ExitStatus())
				}
			}
		}
	}
}