"""Generate Layer-C golden JSON for backend/kubernetes from the 3.8.3 oracle.

Every file written here is `sanitize_for_serialization` of the object the Python
manager submits, i.e. exactly the request body.  The Go test compares its own
marshaled object against these after a canonicalisation that drops empty
arrays, empty objects and nulls (the whole of the typed-vs-untyped difference).
"""
import json
import os
import sys
import uuid
from unittest import mock

sys.path.insert(0, '/root/kathara/kathara-python')

from kubernetes import client
from src.Kathara.model.Lab import Lab
from src.Kathara.model.Machine import Machine
from src.Kathara.model.Link import Link
from src.Kathara.setting.Setting import Setting

OUT = sys.argv[1]
os.makedirs(OUT, exist_ok=True)

VOL0 = "/tmp/kathara-k8s-golden/vol0"
VOL1 = "/tmp/kathara-k8s-golden/vol1"
os.makedirs(VOL0, exist_ok=True)
os.makedirs(VOL1, exist_ok=True)

API = client.ApiClient()

BASE_SETTINGS = {
    'device_prefix': 'devprefix',
    'net_prefix': 'netprefix',
    'device_shell': '/bin/bash',
    'enable_ipv6': False,
    'image_pull_policy': 'Always',
    'host_shared': False,
    'docker_config_json': None,
    'volume_mount_policy': 'Always',
}


def setting(**overrides):
    m = mock.Mock()
    conf = dict(BASE_SETTINGS)
    conf.update(overrides)
    m.configure_mock(**conf)
    return m


def machine_module():
    with mock.patch("kubernetes.client.api.apps_v1_api.AppsV1Api"), \
            mock.patch("kubernetes.client.api.core_v1_api.CoreV1Api"):
        from src.Kathara.manager.kubernetes.KubernetesMachine import KubernetesMachine
        return KubernetesMachine(mock.Mock())


def link_module(seed):
    with mock.patch("kubernetes.client.api.custom_objects_api.CustomObjectsApi"), \
            mock.patch("src.Kathara.manager.kubernetes.KubernetesConfig.KubernetesConfig.get_cluster_user",
                       return_value=seed):
        from src.Kathara.manager.kubernetes.KubernetesLink import KubernetesLink
        return KubernetesLink(mock.Mock())


def base_device(lab=None, **metas):
    lab = lab or Lab("default_scenario")
    device = Machine(lab, "test_device")
    device.add_meta("exec", "ls")
    device.add_meta("mem", "64m")
    device.add_meta("cpus", "2")
    device.add_meta("image", "kathara/test")
    device.add_meta("bridged", False)
    for k, v in metas.items():
        device.add_meta(k, v)
    device.add_meta('real_name', "devprefix-test-device-ec84ad3b")
    return device


def dump(name, obj, replacements=None):
    text = json.dumps(API.sanitize_for_serialization(obj), indent=2, sort_keys=True)
    for old, new in (replacements or {}).items():
        text = text.replace(old, new)
    with open(os.path.join(OUT, name), 'w') as fh:
        fh.write(text + "\n")
    print("wrote", name)


def config_map(name):
    cm = mock.Mock()
    cm.metadata.name = name
    return cm


# --- _build_definition, device NOT run through create() (sysctls empty) ------

with mock.patch("src.Kathara.setting.Setting.Setting.get_instance", return_value=setting()):
    km = machine_module()
    dump("build_no_config.json", km._build_definition(base_device(), None))
    dump("build_config_map.json", km._build_definition(base_device(), config_map("test_device_config_map")))
    dump("build_entrypoint.json", km._build_definition(base_device(entrypoint="/bin/test hello"), None))
    dump("build_args.json", km._build_definition(base_device(args="-n 20 -c 10 -f 30"), None))
    dump("build_entrypoint_args.json",
         km._build_definition(base_device(entrypoint="/bin/test hello", args="-n 20 -c 10 -f 30"), None))

with mock.patch("src.Kathara.setting.Setting.Setting.get_instance",
                return_value=setting(docker_config_json="eyJhdXRocyI6IHt9fQ==")):
    km = machine_module()
    dump("build_docker_config_json.json",
         km._build_definition(base_device(), config_map("test_device_config_map")))

with mock.patch("src.Kathara.setting.Setting.Setting.get_instance", return_value=setting(host_shared=True)):
    km = machine_module()
    dump("build_host_shared.json", km._build_definition(base_device(), None))


# --- create(): the sysctl merge and the real_name assignment ----------------

def captured_create(device, settings_overrides=None, cm=None):
    overrides = settings_overrides or {}
    with mock.patch("src.Kathara.setting.Setting.Setting.get_instance", return_value=setting(**overrides)):
        km = machine_module()
        km.kubernetes_config_map = mock.Mock()
        km.kubernetes_config_map.deploy_for_machine.return_value = cm
        km.client = mock.Mock()
        km.create(device)
        _, kwargs = km.client.create_namespaced_deployment.call_args
        return kwargs['body'], kwargs['namespace']


body, ns = captured_create(base_device())
assert ns == "FwFaxbiuhvSWb2KpN5zw", ns
dump("create_default.json", body)

body, _ = captured_create(base_device(ipv6=True), {'enable_ipv6': True})
dump("create_ipv6.json", body)

device = base_device()
device.add_meta("volume", "%s|/test|ro" % VOL0)
device.add_meta("volume", "%s|/test2|rw" % VOL1)
body, _ = captured_create(device)
dump("create_volumes.json", body, {VOL0: "<VOLUME0>", VOL1: "<VOLUME1>"})

device = base_device()
device.add_meta("volume", "%s|/test|ro" % VOL0)
body, _ = captured_create(device, {'volume_mount_policy': 'Never'})
dump("create_volume_never.json", body, {VOL0: "<VOLUME0>"})

device = base_device()
device.add_meta("port", "3001:56/udp")
with mock.patch("uuid.uuid4", return_value=uuid.UUID("0123456789abcdef0123456789abcdef")):
    body, _ = captured_create(device)
dump("create_ports.json", body)

lab = Lab("default_scenario")
device = base_device(lab=lab)
for cd, mac in (("A", None), ("B", "00:11:22:33:44:55")):
    link = lab.get_or_new_link(cd)
    link.api_object = {"metadata": {"name": "netprefix-%s" % cd.lower()}}
    device.add_interface(link, mac_address=mac)
body, _ = captured_create(device)
dump("create_interfaces.json", body)

device = base_device()
device.add_meta("env", "MY_VAR=value")
device.add_meta("sysctl", "net.ipv4.tcp_syncookies=1")
device.add_meta("shell", "/bin/sh")
body, _ = captured_create(device)
dump("create_env_sysctl_shell.json", body)


# --- KubernetesLink._build_definition ---------------------------------------

with mock.patch("src.Kathara.setting.Setting.Setting.get_instance", return_value=setting()):
    kl = link_module("user123")
    lab = Lab("default_scenario")
    link = lab.get_or_new_link("A")
    dump("nad.json", kl._build_definition(link, 1))

    lab2 = Lab("default_scenario")
    link2 = lab2.get_or_new_link("A_B")
    dump("nad_underscore.json", kl._build_definition(link2, 1362434))


# --- KubernetesSecret --------------------------------------------------------

with mock.patch("src.Kathara.setting.Setting.Setting.get_instance",
                return_value=setting(docker_config_json="eyJhdXRocyI6IHt9fQ==")):
    from src.Kathara.manager.kubernetes.KubernetesSecret import KubernetesSecret
    with mock.patch("kubernetes.client.api.core_v1_api.CoreV1Api"):
        ks = KubernetesSecret()
    ks.client = mock.Mock()
    lab = Lab("Default scenario")
    with mock.patch.object(KubernetesSecret, "_wait_secret_creation", lambda *a, **k: None):
        secrets = ks.create(lab)
    assert lab.hash == "9pe3y6IDMwx4PfOPu5mbNg", lab.hash
    dump("secret.json", secrets[0])


# --- KubernetesConfigMap ------------------------------------------------------

from src.Kathara.manager.kubernetes.KubernetesConfigMap import KubernetesConfigMap

with mock.patch("kubernetes.client.api.core_v1_api.CoreV1Api"):
    kcm = KubernetesConfigMap()
lab = Lab("default_scenario")
device = Machine(lab, "test_device")
device.add_meta('real_name', "devprefix-test-device-ec84ad3b")
device.create_file_from_string("hello\n", "/etc/motd")
cm = kcm._build_for_machine(device)
cm.data = {"hostlab.b64": "<TARBALL>"}
dump("configmap.json", cm)

print("done")
