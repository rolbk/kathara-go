package event

import "github.com/KatharaFramework/kathara-go/model"

// Name is the stable wire name used to subscribe to and dispatch an event.
type Name string

// The 19 events of 3.8.3, in the order the catalog lists them.
const (
	NameDockerImageUpdateFound    Name = "docker_image_update_found"
	NameDockerPullEnded           Name = "docker_pull_ended"
	NameDockerPullProgress        Name = "docker_pull_progress"
	NameDockerPullStarted         Name = "docker_pull_started"
	NameLinkDeployed              Name = "link_deployed"
	NameLinkUndeployed            Name = "link_undeployed"
	NameLinksDeployEnded          Name = "links_deploy_ended"
	NameLinksDeployStarted        Name = "links_deploy_started"
	NameLinksUndeployEnded        Name = "links_undeploy_ended"
	NameLinksUndeployStarted      Name = "links_undeploy_started"
	NameMachineDeployed           Name = "machine_deployed"
	NameMachineStartupWaitEnded   Name = "machine_startup_wait_ended"
	NameMachineStartupWaitStarted Name = "machine_startup_wait_started"
	NameMachineUndeployed         Name = "machine_undeployed"
	NameMachinesDeployEnded       Name = "machines_deploy_ended"
	NameMachinesDeployStarted     Name = "machines_deploy_started"
	NameMachinesUndeployEnded     Name = "machines_undeploy_ended"
	NameMachinesUndeployStarted   Name = "machines_undeploy_started"
	NameMachinesWithVolumes       Name = "machines_with_volumes"
)

type Event interface {
	eventName() Name
}

// Payload constrains every generic entry point of this package ([NameOf],
// [Subscribe], [SubscribeWithHook], [Dispatch], [Unsubscribe], [Subscribers]):
// the 19 payload structs, spelled out as a union, so the type set of E is
// exactly the set of types the table can key and deliver.
type Payload interface {
	Event

	DockerImageUpdateFound | DockerPullEnded | DockerPullProgress | DockerPullStarted |
		LinkDeployed | LinkUndeployed | LinksDeployEnded | LinksDeployStarted |
		LinksUndeployEnded | LinksUndeployStarted | MachineDeployed |
		MachineStartupWaitEnded | MachineStartupWaitStarted | MachineUndeployed |
		MachinesDeployEnded | MachinesDeployStarted | MachinesUndeployEnded |
		MachinesUndeployStarted | MachinesWithVolumes
}

type APIObject any

// ImagePuller is the slice of the Docker image handler that
// [DockerImageUpdateFound] needs. 3.8.3 passes the whole `DockerImage`
// instance (`docker_image=self`, `manager/docker/DockerImage.py:95`) for one
// reason: the subscriber calls `docker_image.pull(image_name)` when the update
// policy says to (`cli/ui/event/UpdateDockerImage.py:23,27`). A backend whose
// own `Pull` takes a context binds it in a two-line adapter rather than putting
// a context in the payload.
type ImagePuller interface {
	Pull(imageName string) error
}

// ---------------------------------------------------------------------------
// Docker image pull
// ---------------------------------------------------------------------------

// DockerPullStarted is dispatched before the pull stream opens
// (`manager/docker/DockerImage.py:58`). It carries no payload;
// `HandleDockerImagePull.init` only builds the progress bar.
type DockerPullStarted struct{}

func (DockerPullStarted) eventName() Name { return NameDockerPullStarted }

// DockerPullProgress is dispatched once per decoded line of the pull stream
// (`manager/docker/DockerImage.py:62`, `progress=progress`).
type DockerPullProgress struct {
	// Progress is the decoded `progressDetail` message.
	Progress PullProgress
}

func (DockerPullProgress) eventName() Name { return NameDockerPullProgress }

// PullProgress is one decoded JSON line of `client.api.pull(..., stream=True,
// decode=True)`, in the shape `HandleDockerImagePull.update` reads it
// (`cli/ui/event/HandleDockerImagePull.py:32-67`).
type PullProgress struct {
	// Status is `progress['status']`, nil when the line carries no `status`
	// key. Only "Downloading" and "Download complete" are acted on; every
	// other status returns early.
	Status *string
	// ID is `progress['id']`, the layer id that keys the per-layer bar. Read
	// only once the status is one of those two, and nil when the key is
	// absent.
	ID *string
	// Detail is `progress['progressDetail']`, nil when the key is absent.
	// 3.8.3 indexes it unguarded on the `Downloading` path
	// (`HandleDockerImagePull.py:58,66`).
	Detail *PullProgressDetail
}

// PullProgressDetail is `progress['progressDetail']`. Both fields are pointers
// because 3.8.3 tests for the key's presence (`'total' in
// progress['progressDetail']`) and passes `None` to rich when it is missing,
// which is an indeterminate bar rather than a bar of length zero — a
// distinction a plain int64 would lose.
type PullProgressDetail struct {
	// Current is `progressDetail['current']`, nil when the key is absent.
	Current *int64
	// Total is `progressDetail['total']`, nil when the key is absent.
	Total *int64
}

// DockerPullEnded is dispatched after the stream is exhausted
// (`manager/docker/DockerImage.py:63`). No payload.
type DockerPullEnded struct{}

func (DockerPullEnded) eventName() Name { return NameDockerPullEnded }

// DockerImageUpdateFound is dispatched when a tagged image's remote digest
// differs from the local one (`manager/docker/DockerImage.py:95-97`).
type DockerImageUpdateFound struct {
	// Image is `docker_image=self` — the handle the subscriber pulls with.
	Image ImagePuller
	// ImageName is `image_name`, the tagged image whose digest moved.
	ImageName string
}

func (DockerImageUpdateFound) eventName() Name { return NameDockerImageUpdateFound }

// ---------------------------------------------------------------------------
// Collision domains
// ---------------------------------------------------------------------------

// LinksDeployStarted opens the collision-domain progress bar
// (`manager/docker/DockerLink.py:64`, `manager/kubernetes/KubernetesLink.py:73`,
// both `items=links`).
type LinksDeployStarted struct {
	Links []*model.Link
}

func (LinksDeployStarted) eventName() Name { return NameLinksDeployStarted }

// Count is `len(items)`, the total `HandleProgressBar.init` gives the bar
// (`cli/ui/event/HandleProgressBar.py:35`).
func (e LinksDeployStarted) Count() int { return len(e.Links) }

type LinkDeployed struct {
	// Link is the collision domain that came up. Both backends pass the model
	// object, with `api_object` already assigned.
	Link *model.Link
}

func (LinkDeployed) eventName() Name { return NameLinkDeployed }

// LinksDeployEnded closes the bar (`manager/docker/DockerLink.py:70`,
// `manager/kubernetes/KubernetesLink.py:85`). No payload.
type LinksDeployEnded struct{}

func (LinksDeployEnded) eventName() Name { return NameLinksDeployEnded }

// LinksUndeployStarted opens the deletion bar
// (`manager/docker/DockerLink.py:176`, `manager/kubernetes/KubernetesLink.py:150`,
// both `items=networks`).
type LinksUndeployStarted struct {
	// Networks are the backend network objects that survived the filters — a
	// Docker `Network` list, already reloaded and pruned to the empty ones
	// (`DockerLink.py:169-170`), or the Kubernetes custom-object dicts. Never
	// the model's Links: by undeploy time the scenario object is gone.
	Networks []APIObject
}

func (LinksUndeployStarted) eventName() Name { return NameLinksUndeployStarted }

// Count is `len(items)` (`cli/ui/event/HandleProgressBar.py:35`).
func (e LinksUndeployStarted) Count() int { return len(e.Networks) }

// LinkUndeployed is dispatched per deleted network
// (`manager/docker/DockerLink.py:217` `item=network`,
// `manager/kubernetes/KubernetesLink.py:196` `item=link_item`).
type LinkUndeployed struct {
	// Network is the backend object that was deleted. Kubernetes dispatches
	// this even when the delete raised an ApiException, which it swallows
	// (`KubernetesLink.py:193-194`).
	Network APIObject
}

func (LinkUndeployed) eventName() Name { return NameLinkUndeployed }

// LinksUndeployEnded closes the deletion bar
// (`manager/docker/DockerLink.py:182`, `manager/kubernetes/KubernetesLink.py:156`).
// No payload.
type LinksUndeployEnded struct{}

func (LinksUndeployEnded) eventName() Name { return NameLinksUndeployEnded }

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// MachinesDeployStarted opens the device progress bar
// (`manager/docker/DockerMachine.py:169`, `manager/kubernetes/KubernetesMachine.py:222`,
// both `items=machines`).
type MachinesDeployStarted struct {
	Machines []*model.Machine
}

func (MachinesDeployStarted) eventName() Name { return NameMachinesDeployStarted }

// Count is `len(items)` (`cli/ui/event/HandleProgressBar.py:35`).
func (e MachinesDeployStarted) Count() int { return len(e.Machines) }

// MachineDeployed is dispatched once a device is up.
type MachineDeployed struct {
	// Machine is the device that came up. Set by the Docker backend, nil on
	// Kubernetes.
	Machine *model.Machine
	// Name is the device's name. Set by the Kubernetes backend, empty on
	// Docker (where the name is on Machine).
	Name string
}

func (MachineDeployed) eventName() Name { return NameMachineDeployed }

// MachinesDeployEnded closes the device bar (`manager/docker/DockerMachine.py:185`).
// Kubernetes only dispatches it when every watched pod reported ready —
// `machines_ready == len(machines)` — so a failed device leaves the bar open
// (`manager/kubernetes/KubernetesMachine.py:281-282`). No payload.
type MachinesDeployEnded struct{}

func (MachinesDeployEnded) eventName() Name { return NameMachinesDeployEnded }

// MachinesUndeployStarted opens the deletion bar
// (`manager/docker/DockerMachine.py:606` `items=containers`,
// `manager/kubernetes/KubernetesMachine.py:621` `items=selected_machines`).
type MachinesUndeployStarted struct {
	Containers []APIObject
	// Names are the device names Kubernetes watches for DELETED pod events —
	// `machines_to_watch`, a set, so its order carries no meaning and only its
	// length is read. Nil on Docker.
	Names []string
}

func (MachinesUndeployStarted) eventName() Name { return NameMachinesUndeployStarted }

// Count is `len(items)` for whichever backend filled the event
// (`cli/ui/event/HandleProgressBar.py:35`); exactly one of the two fields is
// ever set.
func (e MachinesUndeployStarted) Count() int { return len(e.Containers) + len(e.Names) }

// MachineUndeployed is dispatched per deleted device, and varies by backend
// the same way [MachineDeployed] does: Docker passes the container object from
// the undeploy worker (`manager/docker/DockerMachine.py:643`,
// `item=machine_api_object`), Kubernetes the device name from the shutdown
// watcher (`manager/kubernetes/KubernetesMachine.py:633`, `item=machine_name`).
type MachineUndeployed struct {
	// Container is the deleted Docker container. Nil on Kubernetes.
	Container APIObject
	// Name is the deleted device's name. Set by Kubernetes, empty on Docker.
	Name string
}

func (MachineUndeployed) eventName() Name { return NameMachineUndeployed }

// MachinesUndeployEnded closes the deletion bar
// (`manager/docker/DockerMachine.py:612`, `manager/kubernetes/KubernetesMachine.py:638`).
// No payload.
type MachinesUndeployEnded struct{}

func (MachinesUndeployEnded) eventName() Name { return NameMachinesUndeployEnded }

// MachineStartupWaitStarted is dispatched the first time `connect` finds
// `/tmp/EOS` missing, so the CLI can print the "press ENTER to override"
// notice once (`manager/docker/DockerMachine.py:931`, guarded by `printed`).
// No payload.
type MachineStartupWaitStarted struct{}

func (MachineStartupWaitStarted) eventName() Name { return NameMachineStartupWaitStarted }

// MachineStartupWaitEnded is dispatched when the wait resolves, whichever way
// it resolved (`manager/docker/DockerMachine.py:696`); the subscriber clears
// the screen. No payload.
type MachineStartupWaitEnded struct{}

func (MachineStartupWaitEnded) eventName() Name { return NameMachineStartupWaitEnded }

// MachinesWithVolumes is dispatched before deploy when at least one device has
// volumes configured (`manager/docker/DockerMachine.py:157-159`,
// `manager/kubernetes/KubernetesMachine.py:180-182`).
type MachinesWithVolumes struct {
	// Lab is the network scenario, passed so the subscriber can flip
	// `_mount_volumes` on it.
	Lab *model.Lab

	Machines []*model.Machine
}

func (MachinesWithVolumes) eventName() Name { return NameMachinesWithVolumes }
