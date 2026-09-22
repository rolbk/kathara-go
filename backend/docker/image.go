// This file is `DockerImage.py`: is the image here, is it the right
// architecture, is there a newer one, and should we pull.

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/image"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// imageService is `DockerImage` (`DockerImage.py:13`).
type imageService struct {
	manager *Manager
}

// Pull is `DockerImage.pull` (`DockerImage.py:45`): pull the image, dispatching
// the three progress events as the stream arrives.
func (s *imageService) Pull(ctx context.Context, imageName string) error {
	imageName = normalizeImageTag(imageName)

	if err := event.Dispatch(s.manager.dispatcher, event.DockerPullStarted{}); err != nil {
		return err
	}
	// `"Pulling image `%s`... This may take a while." % image_name`
	// (`DockerImage.py:59`). Interpolated, not an slog attribute: the message
	// IS the user-visible line and has to match byte for byte.
	slog.Info("Pulling image `" + imageName + "`... This may take a while.")

	body, err := s.manager.api.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	// `client.api.pull(..., stream=True, decode=True)` yields one decoded JSON
	// object per line; `json.Decoder` over the same body is the same loop.
	decoder := json.NewDecoder(body)
	for {
		var progress pullProgressLine
		if err := decoder.Decode(&progress); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if err := event.Dispatch(s.manager.dispatcher, event.DockerPullProgress{Progress: progress.toEvent()}); err != nil {
			return err
		}
	}

	return event.Dispatch(s.manager.dispatcher, event.DockerPullEnded{})
}

// normalizeImageTag is the tag-defaulting line of `pull`
// (`DockerImage.py:55-56`), including its edge-case behaviour.
func normalizeImageTag(imageName string) string {
	if strings.Contains(imageName, ":") {
		return imageName
	}
	return imageName + ":latest"
}

// pullProgressLine is one decoded line of the pull stream, in the shape
// [event.PullProgress] needs: every field a pointer, because the subscriber
// distinguishes an absent key from an empty one and 3.8.3 raises on the absent
// `status` of an error line (see [event.PullProgress]).
type pullProgressLine struct {
	Status *string `json:"status"`
	ID     *string `json:"id"`
	Detail *struct {
		Current *int64 `json:"current"`
		Total   *int64 `json:"total"`
	} `json:"progressDetail"`
}

func (l pullProgressLine) toEvent() event.PullProgress {
	out := event.PullProgress{Status: l.Status, ID: l.ID}
	if l.Detail != nil {
		out.Detail = &event.PullProgressDetail{Current: l.Detail.Current, Total: l.Detail.Total}
	}
	return out
}

// CheckForUpdates is `DockerImage.check_for_updates` (`DockerImage.py:65`):
// compare the local repo digest against the registry's and, when they differ,
// dispatch `docker_image_update_found` so the CLI's policy handler can decide
// whether to pull.
func (s *imageService) CheckForUpdates(ctx context.Context, imageName string) error {
	// `f"Checking updates for {image_name}..."` (`DockerImage.py:74`).
	slog.Debug("Checking updates for " + imageName + "...")

	if strings.Contains(imageName, "@") {
		// `f"No need to check image digest of {image_name}."`
		// (`DockerImage.py:77`).
		slog.Debug("No need to check image digest of " + imageName + ".")
		return nil
	}

	local, err := s.manager.api.ImageInspect(ctx, imageName)
	if err != nil {
		return err
	}
	if len(local.RepoDigests) == 0 {
		// `f"Image {image_name} is built locally."` (`DockerImage.py:84`).
		slog.Debug("Image " + imageName + " is built locally.")
		return nil
	}

	remote, err := s.manager.api.DistributionInspect(ctx, imageName, "")
	if err != nil {
		return err
	}

	_, localDigest, _ := strings.Cut(local.RepoDigests[0], "@")
	if remote.Descriptor.Digest.String() == localDigest {
		return nil
	}

	return event.Dispatch(s.manager.dispatcher, event.DockerImageUpdateFound{
		Image:     &imagePuller{service: s, ctx: ctx},
		ImageName: imageName,
	})
}

// isDaemonAPIError reports whether err is what docker-py would have raised as
// `docker.errors.APIError` — the only thing `_check_and_pull`'s inner handler
// catches (`DockerImage.py:149-150`).
func isDaemonAPIError(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case isConnectionFailed(err):
		return false
	}
	return isNotFound(err) || strings.HasPrefix(err.Error(), daemonPrefix)
}

// imagePuller adapts [imageService.Pull] to [event.ImagePuller], whose method
// takes no context.
type imagePuller struct {
	service *imageService
	ctx     context.Context
}

func (p *imagePuller) Pull(imageName string) error { return p.service.Pull(p.ctx, imageName) }

// Check is `DockerImage.check` (`DockerImage.py:99`): validate that the name is
// usable WITHOUT pulling anything. It is `_check_and_pull(name, pull=False)`.
func (s *imageService) Check(ctx context.Context, imageName string) error {
	return s.checkAndPull(ctx, imageName, false)
}

func (s *imageService) CheckFromList(ctx context.Context, images []string) error {
	for _, name := range images {
		if err := s.checkAndPull(ctx, name, true); err != nil {
			return err
		}
	}
	return nil
}

// checkAndPull is `DockerImage._check_and_pull` (`DockerImage.py:126`), whose
// control flow is a try/except ladder that reads as a decision table:
func (s *imageService) checkAndPull(ctx context.Context, imageName string, pull bool) error {
	local, err := s.manager.api.ImageInspect(ctx, imageName)
	if err == nil {
		if archErr := checkLocalImageArchitecture(imageName, local.Architecture); archErr != nil {
			return archErr
		}
		if pull {
			if updErr := s.CheckForUpdates(ctx, imageName); updErr != nil {
				if !isDaemonAPIError(updErr) {
					return updErr
				}
				// `logging.debug("Cannot check updates, skipping...")`
				// (`DockerImage.py:150`) names no image, so neither does this.
				slog.Debug("Cannot check updates, skipping...")
			}
		}
		return nil
	}
	if isConnectionFailed(err) {
		// Not an `APIError`, so Python's `except APIError` does not catch it
		// and the daemon-down failure escapes `_check_and_pull` untranslated
		// instead of being reported as a missing image.
		return err
	}

	registryData, err := s.manager.api.DistributionInspect(ctx, imageName, "")
	if err != nil {
		if !isDaemonAPIError(err) {
			// Same asymmetry as the local lookup above: only an APIError
			// reaches the `dial tcp` / not-found fork, so a dead daemon and a
			// cancelled context propagate as themselves instead of becoming
			// "no such image".
			return err
		}
		if isDialTCP(err) {
			return kerrors.NewConnectionImagePull(imageName)
		}
		return kerrors.NewDockerImageNotFound(imageName)
	}

	remoteArchs := make([]string, 0, len(registryData.Platforms))
	for _, platform := range registryData.Platforms {
		remoteArchs = append(remoteArchs, platform.Architecture)
	}
	if archErr := checkRemoteImageArchitecture(imageName, remoteArchs); archErr != nil {
		return archErr
	}

	if pull {
		return s.Pull(ctx, imageName)
	}
	return nil
}

// compatibleArchitectures is the `compatible_archs` set of
// `_check_image_architecture` (`DockerImage.py:190-192`).
func compatibleArchitectures() (host string, compatible []string, err error) {
	host, err = util.GetArchitecture()
	if err != nil {
		return "", nil, err
	}
	if runtime.GOOS == "darwin" {
		return host, []string{host, "amd64"}, nil
	}
	return host, []string{host}, nil
}

// checkLocalImageArchitecture is the `isinstance(image, Image)` arm of
// `_check_image_architecture` (`DockerImage.py:197-198`): a local image has one
// architecture and it must be in the compatible set.
func checkLocalImageArchitecture(imageName, imageArch string) error {
	host, compatible, err := compatibleArchitectures()
	if err != nil {
		return err
	}
	if slices.Contains(compatible, imageArch) {
		return nil
	}
	return kerrors.NewInvalidImageArchitecture(imageName, host)
}

// checkRemoteImageArchitecture is the `isinstance(image, RegistryData)` arm
// (`DockerImage.py:199-204`): a manifest list carries many platforms and ONE
// compatible entry is enough.
func checkRemoteImageArchitecture(imageName string, imageArchs []string) error {
	host, compatible, err := compatibleArchitectures()
	if err != nil {
		return err
	}
	for _, arch := range imageArchs {
		if slices.Contains(compatible, arch) {
			return nil
		}
	}
	return kerrors.NewInvalidImageArchitecture(imageName, host)
}
