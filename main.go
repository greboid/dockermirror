package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kouhin/envflag"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
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

func (m *Mirror) getRegistryRepos(registry string) ([]string, error) {
	repos, err := crane.Catalog(registry)
	if err != nil {
		return nil, err
	}
	namedSpacesRepos := make([]string, 0)
	for index := range repos {
		namedSpacesRepos = append(namedSpacesRepos, fmt.Sprintf("%s/%s", registry, repos[index]))
	}
	return namedSpacesRepos, nil
}

func (m *Mirror) getDockerHubRepos(namespace string) ([]string, error) {
	username, password := m.ResolveString("hub.docker.com")
	resp, err := http.Post("https://hub.docker.com/v2/users/login/", "application/json",
		strings.NewReader(fmt.Sprintf("{\"username\": \"%s\", \"password\": \"%s\"}", username, password)))
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	hubLoginResponse := &HubLoginResponse{}
	err = json.Unmarshal(body, hubLoginResponse)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/?page_size=1000", namespace), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("JWT %s", hubLoginResponse.Token))
	client := http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	repos := &HubRepositoriesResponse{}
	err = json.Unmarshal(body, repos)
	if err != nil {
		return nil, err
	}
	images := make([]string,0)
	for index := range repos.Images {
		images = append(images, fmt.Sprintf("%s/%s", repos.Images[index].Namespace, repos.Images[index].Name))
	}
	return images, nil
}

type HubLoginResponse struct {
	Token string `json:"token"`
	Message string `json:"message"`
}

type HubRepositoriesResponse struct {
	Count int `json:"count"`
	Images []struct {
		User string `json:"user"`
		Name string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"results"`
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
	username, password := m.ResolveString(resource.RegistryStr())
	return authn.FromConfig(authn.AuthConfig{
		Username: username,
		Password: password,
	}), nil
}

func (m *Mirror) ResolveString(resource string) (string, string) {
	var value Registry
	var ok bool
	if resource == name.DefaultRegistry {
		value, ok = m.Registries["hub.docker.com"]
	} else {
		value, ok = m.Registries[resource]
	}
	if !ok {
		return  "", ""
	}
	return value.Username, value.Password
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
