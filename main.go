package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kouhin/envflag"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
)

var (
	configLocation = flag.String("config", "/config.yml", "Specifies the location of the config file")
	duration       = flag.Duration("duration", 0, "Number of seconds between executions")
	limit          = flag.String("rate-limit", "", "The rate at which we mirror images")
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
	parsedLimit, err := parseRate(*limit)
	if err != nil {
		log.Fatalf("Unable to parse rate limit %s", err.Error())
	}
	m := &DockerMirror{
		Images:     config.Images,
		Registries: config.Registries,
		Mirrors:    config.Mirrors,
		Limiter:    rate.NewLimiter(parsedLimit, 1),
	}
	m.Limiter.Limit()
	authn.DefaultKeychain = m
	reposToMirror, err := m.getMirrorRegistries(m.Mirrors)
	if err != nil {
		log.Printf("Unable to get repos from registries to mirrow")
	}
	m.Images = append(m.Images, reposToMirror...)
	if len(m.Images) == 0 {
		log.Fatalf("No images to mirror, exiting.")
	}
	if *duration < time.Minute {
		m.mirrorRepos(m.Images)
		return
	}
	for {
		m.mirrorRepos(m.Images)
		time.Sleep(*duration)
	}
}

func parseRate(rateDescription string) (rate.Limit, error) {
	if len(rateDescription) == 0 {
		return rate.Inf, nil
	}
	parts := strings.Split(rateDescription, "/")
	if len(parts) != 2 {
		return -1, fmt.Errorf("unable to parse rate")
	}
	num, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1, err
	}
	duration, err := time.ParseDuration(parts[1])
	if err != nil {
		return -1, err
	}
	limit := rate.Limit(float64(num) / duration.Seconds())
	return limit, nil
}

func (m *DockerMirror) getMirrorRegistries(registries []Mirror) ([]Image, error) {
	repos := make([]Image, 0)
	for index := range registries {
		newRepos, err := m.getMirrorRegistry(registries[index].From, registries[index].To, registries[index].Namespace)
		if err == nil {
			repos = append(repos, newRepos...)
		}
	}
	return repos, nil
}

func (m *DockerMirror) getMirrorRegistry(source string, dest string, namespace string) ([]Image, error) {
	mirrors := make([]Image, 0)
	repos, err := m.getRepos(source)
	if err != nil {
		log.Fatalf("error parsing mirror registry '%s': %s", source, err.Error())
	}
	for repoIndex := range repos {
		sourceRegistry := repos[repoIndex].Context().RegistryStr()
		repoName := strings.TrimPrefix(repos[repoIndex].Context().Name(), repos[repoIndex].Context().RegistryStr()+"/")
		tag := repos[repoIndex].Identifier()
		destRepo := repoName
		if namespace != "" {
			destRepo = fmt.Sprintf("%s/%s", namespace, repoName)
		}
		mirrors = append(mirrors, Image{
			From: fmt.Sprintf("%s/%s:%s", sourceRegistry, repoName, tag),
			To:   fmt.Sprintf("%s/%s:%s", dest, destRepo, tag),
		})
	}
	return mirrors, nil
}

func (m *DockerMirror) getRepos(registry string) ([]name.Reference, error) {
	parts := strings.SplitN(registry, "/", 2)
	if registry == "hub.docker.com" && len(m.Registries["hub.docker.com"].Username) > 0 && len(parts) == 1 {
		return m.getDockerHubRepos(m.Registries["hub.docker.com"].Username)
	} else if strings.HasPrefix(registry, "hub.docker.com") && len(parts) == 2 {
		return m.getDockerHubRepos(parts[1])
	} else if !strings.HasPrefix(registry, "hub.docker.com") {
		return m.getRegistryRepos(registry)
	}
	return nil, errors.New("invalid format")
}

func (m *DockerMirror) getRegistryRepos(registry string) ([]name.Reference, error) {
	repos, err := crane.Catalog(registry)
	if err != nil {
		return nil, err
	}
	refs := make([]name.Reference, 0)
	for index := range repos {
		image, err := name.ParseReference(fmt.Sprintf("%s/%s", registry, repos[index]))
		if err != nil {
			continue
		}
		refs = append(refs, image)
	}
	return refs, nil
}

func (m *DockerMirror) getDockerHubRepos(namespace string) ([]name.Reference, error) {
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
	images := make([]string, 0)
	for index := range repos.Images {
		images = append(images, fmt.Sprintf("%s/%s", repos.Images[index].Namespace, repos.Images[index].Name))
	}
	refs := make([]name.Reference, 0)
	for index := range images {
		image, err := name.ParseReference(images[index])
		if err != nil {
			continue
		}
		refs = append(refs, image)
	}
	return refs, nil
}

type HubLoginResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}

type HubRepositoriesResponse struct {
	Count  int `json:"count"`
	Images []struct {
		User      string `json:"user"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"results"`
}

type DockerMirror struct {
	Images     []Image
	Registries map[string]Registry
	Mirrors    []Mirror
	Limiter    *rate.Limiter
}

func (m *DockerMirror) mirrorRepos(images []Image) {
	total := len(images)
	log.Printf("Starting to mirror %d images", total)
	failed := 0
	success := 0
	for repo := range images {
		err := m.Limiter.Wait(context.Background())
		if err != nil {
			log.Fatalf("Rate limiter error: %s", err.Error())
		}
		err = crane.Copy(images[repo].From, images[repo].To)
		if err != nil {
			failed++
			log.Printf("mirror %s to %s failed: %s", images[repo].From, images[repo].To, err.Error())
		} else {
			success++
			log.Printf("mirror %s to %s success", images[repo].From, images[repo].To)
		}
	}
	log.Printf("Finished mirroring, %d suceeded, %d failed", success, failed)
}

func (m *DockerMirror) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	username, password := m.ResolveString(resource.RegistryStr())
	return authn.FromConfig(authn.AuthConfig{
		Username: username,
		Password: password,
	}), nil
}

func (m *DockerMirror) ResolveString(resource string) (string, string) {
	var value Registry
	var ok bool
	if resource == name.DefaultRegistry {
		value, ok = m.Registries["hub.docker.com"]
	} else {
		value, ok = m.Registries[resource]
	}
	if !ok {
		return "", ""
	}
	return value.Username, value.Password
}

type Config struct {
	Images     []Image             `yaml:"images"`
	Registries map[string]Registry `yaml:"registries"`
	Mirrors    []Mirror            `yaml:"mirrors"`
}

type Registry struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Image struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type Mirror struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Namespace string `yaml:"namespace"`
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
