package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"

	"github.com/buildpacks/pack/pkg/image"
	"github.com/buildpacks/pack/pkg/logging"
)

const (
	skopeoImageRef = "quay.io/skopeo/stable:latest" //TODO Make this configurable
)

type SkopeoToolExecutor struct {
	imageFetcher ImageFetcher
	logger       logging.Logger
	docker       client.CommonAPIClient
	infoWriter   io.Writer
	errorWriter  io.Writer
}

func newSkopeoToolExecutor(fetcher ImageFetcher, logger logging.Logger, docker client.CommonAPIClient) ImageToolExecutor {
	return SkopeoToolExecutor{
		imageFetcher: fetcher,
		logger:       logger,
		docker:       docker,
		infoWriter:   logging.GetWriterForLevel(logger, logging.InfoLevel),
		errorWriter:  logging.GetWriterForLevel(logger, logging.ErrorLevel),
	}
}

func (s SkopeoToolExecutor) Init(ctx context.Context, options image.FetchOptions) error {
	s.logger.Infof("fetching skopeo tool: %s, required for exporting using OCI layout format", skopeoImageRef)
	skopeoImage, err := s.imageFetcher.Fetch(
		ctx,
		skopeoImageRef,
		options,
	)
	if err != nil {
		return errors.Wrap(err, "fetching skopeo image")
	}
	s.logger.Debugf("skopeo tool %s successfully downloaded", skopeoImage.Name())
	return nil
}

func (s SkopeoToolExecutor) CopyToOCI(ctx context.Context, imgRef string, path string) error {
	_, err := s.mkDirAll(imgRef, path)
	if err != nil {
		return err
	}
	dest := filepath.Join("/oci", imgRef)
	command := []string{"copy", fmt.Sprintf("docker-daemon:%s", imgRef), fmt.Sprintf("oci:%s", dest)}
	s.run(ctx, command, path)
	return nil
}

func (s SkopeoToolExecutor) CopyToDaemon(ctx context.Context, path string, imgRef name.Reference) error {
	ociPath := filepath.Join("/oci", imgRef.String())
	command := []string{"copy", fmt.Sprintf("oci:%s", ociPath), fmt.Sprintf("docker-daemon:%s", imgRef.Name())}
	s.run(ctx, command, path)
	return nil
}

func (s SkopeoToolExecutor) mkDirAll(imgRef string, path string) (string, error) {
	imgRefWithoutTag := strings.SplitN(imgRef, ":", 2)
	destPath := filepath.Join(path, imgRefWithoutTag[0])
	if err := os.MkdirAll(destPath, os.ModePerm); err != nil {
		return "", errors.Wrapf(err, "creating destination path %s", destPath)
	}
	return imgRefWithoutTag[0], nil
}

func (s SkopeoToolExecutor) run(ctx context.Context, command []string, local string) error {
	hostConfig := new(container.HostConfig)
	hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:%s", local, "/oci"), "/var/run/docker.sock:/var/run/docker.sock") //TODO Do this generic

	resp, err := s.docker.ContainerCreate(ctx, &container.Config{
		Image: skopeoImageRef,
		Cmd:   command,
		Tty:   false,
	}, hostConfig, nil, nil, "")
	if err != nil {
		return errors.Wrapf(err, "creating container for running command %s", command)
	}

	if err = s.docker.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return errors.Wrapf(err, "starting container for running command %s", command)
	}
	start := time.Now()
	s.logger.Infof("executing skopeo %s with host bindings: %s", command, hostConfig.Binds)
	statusCh, errCh := s.docker.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err = <-errCh:
		if err != nil {
			return errors.Wrapf(err, "running container excutiong command %s", command)
		}
	case <-statusCh:
	}
	elapsed := time.Since(start)

	s.logger.Infof("skopeo %s operation took %s", command, elapsed)
	if err = s.docker.ContainerRemove(context.Background(), resp.ID, types.ContainerRemoveOptions{Force: true}); err != nil {
		return errors.Wrapf(err, "removing container %s", resp.ID)
	}
	return nil
}
