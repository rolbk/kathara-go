package event

import "github.com/KatharaFramework/kathara-go/model"

// Name is the wire name of an event: the string literal 3.8.3 passes to
// `EventDispatcher.dispatch`, `register` and `unregister`. The names are a
// contract — `cli/ui/event/register.py` subscribes by them and third-party
// code embedding the manager does too — so they are reproduced verbatim even
// where the Go type reads differently.
//
// testdata/catalog.json is the generated proof that this list is exactly the
// set of names 3.8.3 dispatches; TestCatalogMatchesPythonSources walks it.
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

// Event is implemented by every payload struct below, and only by them: the
// method is unexported, so the catalog in this file is the closed set of
// events. That is what replaces `getattr` — [Dispatch] never looks a handler up
// by string, it resolves the subscriber list from the payload's own type
// (PORT_SPEC §3.1 row 13).
//
// Event is the interface the payloads satisfy; [Payload] is the constraint the
// generic entry points take. They are not interchangeable, and the difference
// is load-bearing — see [Payload].
type Event interface {
	eventName() Name
}

// Payload constrains every generic entry point of this package ([NameOf],
// [Subscribe], [SubscribeWithHook], [Dispatch], [Unsubscribe], [Subscribers]):
// the 19 payload structs, spelled out as a union, so the type set of E is
// exactly the set of types the table can key and deliver.
//
// Naming them a second time buys the property the dispatcher's invariants rest
// on: the [Name] a subscription is filed under and the func type stored with it
// determine each other. E is one of 19 struct types, each reports a distinct
// name, so a subscription filed under `NameOf[E]()` always holds a
// `func(E) error` and the assertion in [Dispatch] cannot fail. A 20th event has
// to be added here as well, or `NameOf` on it does not compile and
// TestCatalogMatchesPythonSources cannot see it.
//
// It also turns the two instantiations that are otherwise silent hazards into
// compile errors:
//
//	Subscribe(d, func(e *LinkDeployed) error { … })
//	// *LinkDeployed does not satisfy Payload (*LinkDeployed missing in …)
//
//	var e Event = LinkDeployed{}
//	Dispatch(d, e)
//	// Event does not satisfy Payload
//
// The pointer instantiation would otherwise panic inside [NameOf] — a value
// method reached through a nil `*LinkDeployed`. The interface instantiation
// would otherwise look the subscriber list up correctly and then match none of
// its handles, dropping every subscriber and returning nil. Neither has a
// Python analogue: `dispatch` is keyed by a string and always reaches the
// callbacks it stored, and a lost event there is impossible. Both are Go-only
// accidents, so the port makes them not compile (PORT_SPEC §10).
type Payload interface {
	Event

	DockerImageUpdateFound | DockerPullEnded | DockerPullProgress | DockerPullStarted |
		LinkDeployed | LinkUndeployed | LinksDeployEnded | LinksDeployStarted |
		LinksUndeployEnded | LinksUndeployStarted | MachineDeployed |
		MachineStartupWaitEnded | MachineStartupWaitStarted | MachineUndeployed |
		MachinesDeployEnded | MachinesDeployStarted | MachinesUndeployEnded |
		MachinesUndeployStarted | MachinesWithVolumes
}

// APIObject is a backend-native handle that an event carries opaquely: a
// `docker.models.networks.Network`, a `docker.models.containers.Container`, or
// the dict a Kubernetes custom-object list returns. 3.8.3 hands the SDK object
// straight to the subscriber and no subscriber ever reads it —
// `HandleProgressBar.update` ignores its `item` argument entirely
// (`cli/ui/event/HandleProgressBar.py:37-47`) — so the payload stays opaque
// instead of pulling the backend SDK types down here, which PACKAGE_GRAPH.md
// §1.2 forbids: `backend/*` sits above `event`, never below it.
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
//
// # Why every field is a pointer
//
// Each one is a dict key the handler indexes with no guard, and the daemon does
// not put them all on every line. `status` is the one that matters: a pull that
// fails after the stream opened — layer download error, registry auth expiry,
// no disk left — is reported as a line `{"errorDetail": {…}, "error": "…"}`
// carrying no `status` key, and docker-py hands it through untouched
// (`APIClient.pull` only `_raise_for_status`es the initial response, then
// `_stream_helper` decodes and yields whatever arrives;
// `manager/docker/DockerImage.py:61-62` iterates that generator). So
// `progress['status']` raises `KeyError: 'status'`, which — there being no try
// block anywhere on the path — unwinds out of `dispatch` and out of
// `DockerImage.pull`. Oracle-probed on 3.8.3: `update({'errorDetail': {…},
// 'error': …})` raises `KeyError: 'status'` and `dispatch` propagates it.
//
// That is a 3.8.3 bug — a failed pull crashes instead of reporting — and the
// port reproduces bugs rather than fixing them (PORT_SPEC §0.1). A plain
// `string` cannot: it collapses the absent key to `""`, the subscriber falls
// into the handler's `else: return` branch, and the failed pull goes on to
// report success through `docker_pull_ended`.
//
// So nil means the key was absent, and a subscriber must do to it what CPython
// does to a dict indexed without it. [PullProgressDetail] already carries its
// keys this way for a milder reason (`'total' in progress['progressDetail']`).
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
//
// This is the one event whose subscriber can fail: `UpdateDockerImage.run`
// pulls the new image, and a pull that raises propagates out of `dispatch`
// into `check_for_updates` and up the deploy path. [Dispatch] returns that
// error for the same reason.
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
	// Links are the collision domains about to be created, in the order the
	// backend submits them (ORDERING.tsv rows 47 and 80: lab insertion order,
	// preserved through the selected/excluded filters). Python passes the
	// `dict_items` of `(name, Link)` pairs; the names live on the Link here.
	Links []*model.Link
}

func (LinksDeployStarted) eventName() Name { return NameLinksDeployStarted }

// Count is `len(items)`, the total `HandleProgressBar.init` gives the bar
// (`cli/ui/event/HandleProgressBar.py:35`).
func (e LinksDeployStarted) Count() int { return len(e.Links) }

// LinkDeployed is dispatched by the worker that created the network
// (`manager/docker/DockerLink.py:93`, `manager/kubernetes/KubernetesLink.py:103`,
// both `item=link`). It arrives on a worker goroutine and in completion order,
// which is nondeterministic by design (ORDERING.tsv row 48).
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
//
// On Kubernetes it is dispatched from the watcher goroutine, not the caller's
// (CONCURRENCY.tsv row 14).
type MachinesDeployStarted struct {
	// Machines are the devices about to be deployed, in submission order —
	// `lab.dep` order when the scenario has dependencies, lab insertion order
	// otherwise (ORDERING.tsv row 31). Python passes `(name, Machine)` pairs.
	Machines []*model.Machine
}

func (MachinesDeployStarted) eventName() Name { return NameMachinesDeployStarted }

// Count is `len(items)` (`cli/ui/event/HandleProgressBar.py:35`).
func (e MachinesDeployStarted) Count() int { return len(e.Machines) }

// MachineDeployed is dispatched once a device is up. It is the one event with
// two subscribers in the stock CLI, and their order is load-bearing: the
// progress bar advances before the terminal opens (`register.py:72,81`,
// ORDERING.tsv row 96).
//
// Its payload is the port's clearest case of per-backend variance: Docker
// dispatches the device object from the deploy worker
// (`manager/docker/DockerMachine.py:204`, `item=machine`) while Kubernetes
// dispatches the device *name* from the pod watcher
// (`manager/kubernetes/KubernetesMachine.py:260`, `item=machine_name`). In
// Python that mismatch would blow up in `HandleMachineTerminal.run`, which
// calls `item.get_num_terms()` (`cli/ui/event/HandleMachineTerminal.py:23`);
// it never does, because Megalos force-disables terminals first
// (`manager/kubernetes/KubernetesMachine.py:174`). Both fields are kept so a
// subscriber can tell the two apart instead of crashing on one of them.
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
	// Containers are the Docker container objects to delete, in Docker API
	// list order (ORDERING.tsv row 43). Nil on Kubernetes.
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
//
// The subscriber prints the host-path/guest-path tree and, under the `Prompt`
// policy, sets the scenario option `_mount_volumes` to false when the user
// declines — which is why the [Lab] is in the payload at all
// (`cli/ui/event/MountDevicesVolumes.py:15-39`).
type MachinesWithVolumes struct {
	// Lab is the network scenario, passed so the subscriber can flip
	// `_mount_volumes` on it.
	Lab *model.Lab
	// Machines are the devices that have volumes, in scenario insertion order
	// (ORDERING.tsv row 98 — the print order is observable). Python passes a
	// `dict[str, Machine]` filtered out of the deploy list.
	Machines []*model.Machine
}

func (MachinesWithVolumes) eventName() Name { return NameMachinesWithVolumes }
