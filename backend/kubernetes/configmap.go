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
const MaxConfigMapSize = 3145728

// hostlabKey is the ConfigMap key the postStart hook reads
// (`KubernetesConfigMap.py:95`, and `/tmp/kathara/hostlab.b64` in startup.go).
// The mount path and key together determine the file name inside the pod.
const hostlabKey = "hostlab.b64"

// configMapService is `KubernetesConfigMap` (`KubernetesConfigMap.py:15`).
type configMapService struct {
	clientset kubernetes.Interface
}

// DeployForMachine is `deploy_for_machine` (`KubernetesConfigMap.py:22`):
// build, and submit if there is anything to submit.
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
func (s *configMapService) DeleteForMachine(ctx context.Context, machineName, namespace string) {
	name := ConfigMapName(machineName, namespace)
	if err := s.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {

		_ = err
	}
}

// buildForMachine is `_build_for_machine` (`KubernetesConfigMap.py:68`).
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
