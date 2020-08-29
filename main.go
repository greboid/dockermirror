package main

import (
	"flag"
	"fmt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kouhin/envflag"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"time"
)

var (
	configLocation = flag.String("config", "/config.yml",
		"Specifies the location of the config file")
	duration = flag.Duration("duration", 0, "Number of seconds between executions")
)

func main() {
	err := envflag.Parse()
	if err != nil {
		log.Fatalf("unable to parse config location: %s", err.Error())
	}
	config, err := parseConfig(*configLocation)
	if err != nil {
		log.Fatalf("unable to load the config file: %s", err.Error())
	}
	m := &Mirror{
		Images:     config.Images,
		Registries: config.Registries,
	}
	authn.DefaultKeychain = m
	if len(m.Images) == 0 {
		log.Fatalf("No images to mirror, exiting.")
	}
	if *duration < time.Minute {
		m.mirrorRepos()
		return
	}
	for {
		m.mirrorRepos()
		time.Sleep(*duration)
	}
}

type Mirror struct {
	Images         []Image
	Registries     map[string]Registry
}

func (m *Mirror) mirrorRepos() {
	log.Printf("Starting to mirror %d images", len(m.Images))
	failed := 0
	success := 0
	for repo := range m.Images {
		err := crane.Copy(m.Images[repo].From, m.Images[repo].To)
		if err != nil {
			failed++
			log.Printf("Mirror %s to %s failed: %s", m.Images[repo].From, m.Images[repo].To, err.Error())
		} else {
			success++
			log.Printf("Mirror %s to %s success", m.Images[repo].From, m.Images[repo].To)
		}
	}
	log.Printf("Finished mirroring, %d suceeded, %d failed", success, failed)
}

func (m *Mirror) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	var value Registry
	var ok bool
	if resource.RegistryStr() == name.DefaultRegistry {
		value, ok = m.Registries["hub.docker.com"]
	} else {
		value, ok = m.Registries[resource.RegistryStr()]
	}
	if !ok {
		return authn.FromConfig(authn.AuthConfig{
			Username: "",
			Password: "",
		}), nil
	}
	return authn.FromConfig(authn.AuthConfig{
		Username: value.Username,
		Password: value.Password,
	}), nil
}

type Config struct {
	Images     []Image             `yaml:"images"`
	Registries map[string]Registry `yaml:"registries"`
}

type Registry struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Image struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type ReferenceImage struct {
	From name.Reference
	To   name.Reference
}

func parseConfig(configPath string) (*Config, error) {
	s, err := os.Stat(configPath)
	if err != nil {
		return nil, fmt.Errorf("'%s' does not exist", configPath)
	}
	if s.IsDir() {
		return nil, fmt.Errorf("'%s' is not a file", configPath)
	}
	config := &Config{}
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open '%s'", configPath)
	}
	defer func() {
		_ = file.Close()
	}()
	if err := yaml.NewDecoder(file).Decode(config); err != nil {
		return nil, fmt.Errorf("parse error: %s", err.Error())
	}
	return config, nil
}
