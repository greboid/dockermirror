### Docker registry mirror

CLI utility to mirror docker images from one registry to another.

## Docker usage

Available as a docker image, will access either CLI arguments or environmental variables for configuration.

```
version: '3.7'

services:
  dockermirror:
    image: greboid/dockermirror
    environment:
      CONFIG: /config.yml
      DURATION: 6h
    volumes:
      - <local path to config.yml>:/config.yml
    restart: always
```

## Basic CLI Usage

This can also be installed and run directly:

```
go install github.com/greboid/dockermirror
```
    
```
  dockermirror \
    --config [path to config.yml file]  \
    --duration [repeat every X duration]
```

## Config Format

The configuration file has a list of images to mirror, and registry credentials.  Whilst the registries section needs 
to include hub.docker.com if you want to pull or push private images, image names default to docker hub as in the 
docker cli.

```
---
images:
  - from: <source image>
    to: <destination image>
mirrors:
  - from: hub.docker.com
    to: <custom repository>
registries:
  hub.docker.com:
    username: "exampleUsername"
    password: "examplePassword"
  <custom repository>:
    username: "exampleUsername"
    password: "examplePassword"
```