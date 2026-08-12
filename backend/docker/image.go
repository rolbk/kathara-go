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
//
// The tag-defaulting line carries a bug the port keeps
// (docker-backend.md gotcha 4):
//
//	if (':' or '@') not in image_name: image_name = "%s:latest" % image_name
//
// `(':' or '@')` evaluates to `':'` before the `in` runs, so only the colon is
// ever tested. The `@` never participates. It happens not to matter — a digest
// reference is `name@sha256:…` and contains a colon — but a "fix" would change
// which string reaches the daemon, so the colon test stands alone here as it
// does there.
//
// A second consequence of testing the whole string: a registry host with a port
// (`localhost:5000/img`) already contains a colon, so it is NOT tagged
// `:latest` and the daemon's own default applies instead. Same in both.
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
// (`DockerImage.py:55-56`), bug included.
func normalizeImageTag(imageName string) string {
	if strings.Contains(imageName, ":") {
		return imageName
	}
	return imageName + ":latest"
}

// pullProgressLine is one decoded line of the pull stream, in the shape
// [event.PullProgress] needs: every field a pointer, because the subscriber
// distinguishes an absent key from an empty one and 3.8.3 crashes on the
// absent `status` of an error line (see [event.PullProgress]).
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
//
// Three early returns, in Python's order:
//
//   - a digest reference (`@` anywhere in the name) needs no check;
//   - an image with no `RepoDigests` was built locally and has nothing to
//     compare against;
//   - equal digests.
//
// `local_repo_digests[0]` is `name@sha256:…` and Python splits on `"@"` and
// takes the second half, so a name that itself contains an `@` — which a
// reference cannot — is not a concern. The split is `str.split`, which yields
// every part; Python unpacks exactly two and would ValueError on three.
// SplitN(2) here keeps the first `@` as the separator, which is the same result
// for every reachable input.
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
//
// The test is by KIND, not by origin, because Python's is: the handler wraps
// the whole `check_for_updates` call, so it swallows a daemon failure wherever
// inside it happened — including the `pull` the `docker_image_update_found`
// subscriber performs under the `Prompt`/`Always` policy
// (`cli/ui/event/UpdateDockerImage.py:23,27`). A Hub rate-limit during that
// pull is a debug line and the deploy continues on the stale image.
//
// What is NOT an APIError, and therefore escapes `_check_and_pull` entirely:
// `requests.exceptions.ConnectionError` when the daemon dies mid-check, and —
// with no Python counterpart but the same obligation — a cancelled context,
// which JSON_CLI_CONTRACT.md §6.2 requires to surface as cancellation. A
// subscriber failure that is not a daemon answer (a terminal read failing under
// the `Prompt` policy) is none of docker-py's exception types either and
// escapes for the same reason.
//
// The positive signal is the daemon's own wrapper: the SDK prefixes every
// error it builds from a non-2xx response with [daemonPrefix], which is the
// same fact [explanation] is written against. [isNotFound] is the second arm,
// for the 404s the SDK synthesises WITHOUT asking the daemon —
// `ImageInspect("")` and `DistributionInspect("")` short-circuit to an
// unprefixed not-found where docker-py would have posted the empty reference
// and been answered 404, i.e. `docker.errors.NotFound`, an APIError.
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
//
// Python passes `docker_image=self`, the whole `DockerImage` instance, so the
// subscriber can call `docker_image.pull(image_name)`
// (`cli/ui/event/UpdateDockerImage.py:23,27`). The context is bound here rather
// than put in the payload because `event` sits below `backend/docker` in the
// import graph and must not grow a context parameter for one backend's benefit
// ([event.ImagePuller]).
//
// Carrying a context in a struct is the exception PORT_SPEC §0.2 #11 leaves
// room for: the value never outlives the [imageService.CheckForUpdates] call
// that built it, because `dispatch` is synchronous and the subscriber runs
// inside it.
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

// CheckFromList is `DockerImage.check_from_list` (`DockerImage.py:114`): the
// deploy path's image pass, sequential by design (CONCURRENCY.tsv "Sequential
// by design: … DockerImage.check_from_list (image pulls)").
//
// Python receives a `set` here and iterates it, so the order of the checks —
// and therefore of the pull progress bars and the update prompts — is
// hash-randomised per run (ORDERING.tsv rows 29 and 46, both flagged "!!").
// The register's instruction is "accept ordered slice param; fix producer",
// which is what this signature is: the caller ([machineService.DeployMachines])
// dedupes preserving first occurrence.
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
//
//	local hit  + pull=false → arch-check, done
//	local hit  + pull=true  → arch-check, then update check (APIError there is
//	                          swallowed with a debug line — the check is
//	                          best-effort, the deploy is not, and the SUBSCRIBER's
//	                          own pull is inside the same handler; see
//	                          [isDaemonAPIError])
//	local miss              → registry lookup, arch-check that, pull iff pull
//
// The two `except InvalidImageArchitectureError: raise e` arms are identity
// re-raises that exist to stop the enclosing `except APIError` from swallowing
// them — `InvalidImageArchitectureError` is-a `ValueError`, not an `APIError`,
// so they are dead in Python too. They are noted rather than reproduced,
// because Go's error returns have no enclosing handler to escape.
//
// The registry failure splits on the daemon's text: a 500 whose explanation
// mentions `dial tcp` is "no internet", anything else is "no such image"
// (`:161-168`).
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
//
// The host architecture is `utils.get_architecture()`, which reads the KERNEL's
// architecture and maps it to Docker's names — not `runtime.GOARCH`, which
// would report the binary's and differ under emulation or a 32-bit build on a
// 64-bit kernel (OQ-20, PACKAGE_GRAPH.md §4).
//
// macOS additionally accepts `amd64`, because Rosetta runs those images on an
// arm64 host. That is the one place the three platforms do not agree, and it is
// an inline `runtime.GOOS` test rather than a build-tagged file
// (PACKAGE_GRAPH.md §4, last line).
// The error is `utils.get_architecture`'s own: it reads the kernel through
// uname, which can fail where Python's `platform.machine()` answers "".
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
//
// The error names `host_arch`, not the compatible set, so a Rosetta-capable
// host that finds neither arm64 nor amd64 still reports arm64.
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
