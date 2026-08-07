package kubernetes

import "testing"

// TestResourceName is EXPECTATIONS-k8s §1 "get_deployment_name" and §2
// "get_network_name": the same eight lines twice, so one table covers both.
//
// The md5-8 values are the oracle's (`test_device`→`ec84ad3b`,
// `device_name`→`3b92d741`, `a_b`→`dbf08e00`).
func TestResourceName(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		object string
		want   string
	}{
		{
			name:   "plain name",
			prefix: "devprefix", object: "device", want: "devprefix-device",
		},
		{
			// The underscore in the NAME triggers the hash suffix; the one in
			// the PREFIX is silently deleted by the character filter and
			// triggers nothing.
			name:   "underscore in both name and prefix",
			prefix: "dev_prefix", object: "device_name", want: "devprefix-device-name-3b92d741",
		},
		{
			name:   "invalid characters are deleted, letters are folded",
			prefix: "devprefix", object: "Device05#A", want: "devprefix-device05a",
		},
		{
			name:   "collision-domain baseline",
			prefix: "netprefix", object: "a", want: "netprefix-a",
		},
		{
			name:   "collision-domain underscore",
			prefix: "netprefix", object: "a_b", want: "netprefix-a-b-dbf08e00",
		},
		{
			name:   "collision-domain invalid characters",
			prefix: "netprefix", object: "A05#", want: "netprefix-a05",
		},
		{
			name:   "device fixture name",
			prefix: "devprefix", object: "test_device", want: "devprefix-test-device-ec84ad3b",
		},
		{
			// The md5 is over the ORIGINAL name, so `a_b` and `a-b` do not
			// collide even though the replacement makes them look alike.
			name:   "hyphen name does not collide with the underscore one",
			prefix: "netprefix", object: "a-b", want: "netprefix-a-b",
		},
		{
			// A dot survives: a Kubernetes name is an RFC 1123 subdomain.
			name:   "dots survive",
			prefix: "devprefix", object: "a.b", want: "devprefix-a.b",
		},
		{
			// Runs of disallowed characters collapse to nothing at all.
			name:   "runs of invalid characters",
			prefix: "devprefix", object: "a###b", want: "devprefix-ab",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResourceName(test.prefix, test.object); got != test.want {
				t.Errorf("ResourceName(%q, %q) = %q, want %q", test.prefix, test.object, got, test.want)
			}
		})
	}
}

// TestDeploymentAndNetworkName pins that the two named wrappers really are the
// same function, which is what makes a device and a collision domain of the
// same name collide only through their prefixes.
func TestDeploymentAndNetworkName(t *testing.T) {
	if got, want := DeploymentName("devprefix", "test_device"), "devprefix-test-device-ec84ad3b"; got != want {
		t.Errorf("DeploymentName = %q, want %q", got, want)
	}
	if got, want := NetworkName("netprefix", "a_b"), "netprefix-a-b-dbf08e00"; got != want {
		t.Errorf("NetworkName = %q, want %q", got, want)
	}
}

// TestConfigMapName is `KubernetesConfigMap.build_name_for_machine`
// (`KubernetesConfigMap.py:55`), whose first argument is the DEPLOYMENT name
// and not the device name.
func TestConfigMapName(t *testing.T) {
	got := ConfigMapName("devprefix-test-device-ec84ad3b", "FwFaxbiuhvSWb2KpN5zw")
	want := "devprefix-test-device-ec84ad3b-FwFaxbiuhvSWb2KpN5zw-files"
	if got != want {
		t.Errorf("ConfigMapName = %q, want %q", got, want)
	}
}

// TestObjectSelector is the exact label-selector string both listing functions
// build (EXPECTATIONS-k8s
// `test_get_machines_api_objects_by_filter_machine_name`).
func TestObjectSelector(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no name", input: "", want: "app=kathara"},
		{name: "with a name", input: "test_device", want: "app=kathara,name=test_device"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ObjectSelector(test.input); got != test.want {
				t.Errorf("ObjectSelector(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// TestObjectLabels pins the label scheme: `name` is the KATHARÁ name, never the
// mangled Kubernetes one, and `app` is the constant every listing filters on.
func TestObjectLabels(t *testing.T) {
	labels := ObjectLabels("test_device")
	if labels[labelName] != "test_device" {
		t.Errorf("name label = %q, want %q", labels[labelName], "test_device")
	}
	if labels[labelApp] != "kathara" {
		t.Errorf("app label = %q, want %q", labels[labelApp], "kathara")
	}
	if len(labels) != 2 {
		t.Errorf("labels = %v, want exactly two keys", labels)
	}
}

// TestNamespaceSelector pins the `kubernetes.io/metadata.name` selector that
// makes a missing namespace an empty list instead of a 404.
func TestNamespaceSelector(t *testing.T) {
	got := namespaceSelector("FwFaxbiuhvSWb2KpN5zw")
	want := "kubernetes.io/metadata.name=FwFaxbiuhvSWb2KpN5zw"
	if got != want {
		t.Errorf("namespaceSelector = %q, want %q", got, want)
	}
}

// TestSanitizeUTF8 pins the `errors='ignore'` on the md5 input: an invalid
// encoding is dropped rather than replaced, so a name carrying a stray byte
// hashes the way CPython hashes it.
func TestSanitizeUTF8(t *testing.T) {
	if got := string(sanitizeUTF8("a\xffb")); got != "ab" {
		t.Errorf("sanitizeUTF8 = %q, want %q", got, "ab")
	}
	if got := string(sanitizeUTF8("aéb")); got != "aéb" {
		t.Errorf("sanitizeUTF8 = %q, want %q", got, "aéb")
	}
}
