// This file is `KubernetesConfigMap.py`: the device's files, packed, base64'd
// and handed to the cluster as a ConfigMap that the pod mounts at
// `/tmp/kathara` and the postStart hook unpacks.

package kubernetes

import (
	"context"
	"encoding/base64"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// MaxConfigMapSize is `MAX_FILE_SIZE` (`KubernetesConfigMap.py:12`): 3 MiB of
// TAR bytes, measured BEFORE the base64 expansion.
//
// The real ceiling is the API server's 1 MiB object limit, which the base64 of
// 3 MiB blows past by a factor of four — so a device between roughly 768 KiB
// and 3 MiB passes this check and is rejected by the cluster instead. Ported
// as-is: it is the number the error message quotes and PORT_SPEC §0.4 freezes
// the message.
const MaxConfigMapSize = 3145728

// hostlabKey is the ConfigMap key the postStart hook reads
// (`KubernetesConfigMap.py:95`, and `/tmp/kathara/hostlab.b64` in startup.go).
// The mount path plus this key IS the file name inside the pod, so the two are
// one contract.
const hostlabKey = "hostlab.b64"

// configMapService is `KubernetesConfigMap` (`KubernetesConfigMap.py:15`).
type configMapService struct {
	clientset kubernetes.Interface
}

// DeployForMachine is `deploy_for_machine` (`KubernetesConfigMap.py:22`):
// build, and submit if there is anything to submit.
//
// A device with no files answers (nil, nil) — Python's None — and
// [machineService.Create] then builds a Deployment with no hostlab volume and
// no hostlab mount, which is what makes the postStart hook's
// `if [ -f "/tmp/kathara/hostlab.b64" ]` branch false.
//
// Errors: [kerrors.ErrKubernetesConfigMap] for an oversized device folder, and
// the API server's own — which [machineService.Create] catches, because the
// create is inside its `try` (`KubernetesMachine.py:359-370`).
func (s *configMapService) DeployForMachine(ctx context.Context, machine *model.Machine) (*corev1.ConfigMap, error) {
	configMap, err := s.buildForMachine(machine)
	if err != nil {
		return nil, err
	}
	if configMap == nil {
		return nil, nil
	}

	return s.clientset.CoreV1().ConfigMaps(machine.Lab.Hash).Create(ctx, configMap, metav1.CreateOptions{})
}

// DeleteForMachine is `delete_for_machine` (`KubernetesConfigMap.py:38`).
//
// Every API failure is swallowed, 404 included: `_delete_machine` calls this
// for every pod it tears down and a device that shipped no files never had a
// ConfigMap to delete, so a not-found is the normal case rather than an error.
//
// machineName here is the DEPLOYMENT name, not the device name — see
// [ConfigMapName].
func (s *configMapService) DeleteForMachine(ctx context.Context, machineName, namespace string) {
	name := ConfigMapName(machineName, namespace)
	if err := s.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		// `except ApiException: return` — and nothing else is caught, so a
		// transport failure propagates in Python. It cannot here: the signature
		// has nowhere to put it, and Python's own caller
		// (`_delete_machine`) would have let it kill one worker of the undeploy
		// fan-out. Recorded in DIVERGENCES.md.
		_ = err
	}
}

// buildForMachine is `_build_for_machine` (`KubernetesConfigMap.py:68`).
//
// The size check is on the TAR bytes and the stored value is their base64, so
// the ConfigMap is a third larger than the number the check tested. Both sizes
// in the error are rendered by `utils.human_readable_bytes`
// ([util.HumanReadableBytes]).
//
// `deletion_grace_period_seconds=0` on the metadata is Python's
// (`KubernetesConfigMap.py:98`) and has no effect on a ConfigMap — the field is
// only meaningful on an object being deleted — but it is in the submitted
// object and therefore in the golden.
func (s *configMapService) buildForMachine(machine *model.Machine) (*corev1.ConfigMap, error) {
	tarData, err := packData(machine)
	if err != nil {
		return nil, err
	}
	if len(tarData) == 0 {
		return nil, nil
	}

	if len(tarData) > MaxConfigMapSize {
		maxSize, err := util.HumanReadableBytes(MaxConfigMapSize)
		if err != nil {
			return nil, err
		}
		current, err := util.HumanReadableBytes(int64(len(tarData)))
		if err != nil {
			return nil, err
		}
		return nil, kerrors.NewConfigMapTooLarge(maxSize, current)
	}

	gracePeriod := int64(0)
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:                       ConfigMapName(realName(machine), machine.Lab.Hash),
			DeletionGracePeriodSeconds: &gracePeriod,
		},
		Data: map[string]string{hostlabKey: base64.StdEncoding.EncodeToString(tarData)},
	}, nil
}
