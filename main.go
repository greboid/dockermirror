package main

import (
	"flag"
	"fmt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
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
	m := Mirror{
		Images:     config.Images,
		Registries: config.Registries,
	}
	err = m.parseImages()
	if err != nil {
		log.Fatalf("Unable to parse image names: %s", err.Error())
	}
	if len(m.imageRefereces) == 0 {
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
	imageRefereces []ReferenceImage
	Registries     map[string]Registry
}

func (m *Mirror) parseImages() error {
	for repo := range m.Images {
		fromRef, err := m.parseRef(m.Images[repo].From)
		if err != nil {
			return fmt.Errorf("unable to parse %s to an image", m.Images[repo].From)
		}
		toRef, err := m.parseRef(m.Images[repo].To)
		if err != nil {
			return fmt.Errorf("unable to parse %s to an image", m.Images[repo].To)
		}
		m.imageRefereces = append(m.imageRefereces, ReferenceImage{
			From: fromRef,
			To:   toRef,
		})
	}
	return nil
}

func (m *Mirror) mirrorRepos() {
	log.Printf("Starting to mirror %d images", len(m.imageRefereces))
	failed := 0
	success := 0
	for repo := range m.imageRefereces {
		err := m.mirrorRepo(m.imageRefereces[repo].From, m.imageRefereces[repo].To)
		if err != nil {
			failed++
			log.Printf("Mirror %s to %s failed: %s", m.imageRefereces[repo].From, m.imageRefereces[repo].To, err.Error())
		} else {
			success++
			log.Printf("Mirror %s to %s success", m.imageRefereces[repo].From, m.imageRefereces[repo].To)
		}
	}
	log.Printf("Finished mirroring, %d suceeded, %d failed", success, failed)
}

func (m *Mirror) mirrorRepo(from name.Reference, to name.Reference) error {
	source, err := m.getImage(from)
	if err != nil {
		return fmt.Errorf("unable to get %s: %s", from.Context(), err.Error())
	}
	err = m.pushImage(to, source)
	if err != nil {
		return fmt.Errorf("unable to push %s: %s", from.Context(), err.Error())
	}
	return nil
}

func (m *Mirror) parseRef(imageName string) (name.Reference, error) {
	ref, err := name.ParseReference(imageName)
	if err != nil {
		return nil, err
	}
	return ref, nil
}

func (m *Mirror) getImage(imageName name.Reference) (v1.Image, error) {
	image, err := remote.Image(imageName, remote.WithAuth(m.getImageauth(imageName)))
	return image, err
}

func (m *Mirror) pushImage(name name.Reference, image v1.Image) error {
	return remote.Write(name, image, remote.WithAuth(m.getImageauth(name)))
}

func (m *Mirror) getImageauth(ref name.Reference) authn.Authenticator {
	var value Registry
	var ok bool
	if ref.Context().Registry.Name() == name.DefaultRegistry {
		value, ok = m.Registries["hub.docker.com"]
	} else {
		value, ok = m.Registries[ref.Context().Registry.Name()]
	}
	if !ok {
		return authn.FromConfig(authn.AuthConfig{
			Username: "",
			Password: "",
		})
	}
	return authn.FromConfig(authn.AuthConfig{
		Username: value.Username,
		Password: value.Password,
	})
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
