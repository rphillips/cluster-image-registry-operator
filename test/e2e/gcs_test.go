package e2e

import (
	operatorapi "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"reflect"
	"testing"
	"time"

	imageregistryv1 "github.com/openshift/cluster-image-registry-operator/pkg/apis/imageregistry/v1"
	"github.com/openshift/cluster-image-registry-operator/test/framework"
)

var (
	// Invalid GCS credentials data
	fakeGCSKeyfile = `{
  "type": "service_account",
  "project_id": "openshift-test-project",
  "private_key_id": "aabbccddeeffgghhiijjkkllmmnnooppqqrrssttuuvvwwxxyyzz",
  "private_key": "-----BEGIN PRIVATE KEY-----\n556B58703273357638792F423F4528482B4D6251655468566D597133743677397A24432646294A404E635266556A586E5A7234753778214125442A472D4B6150645367566B59703373357638792F423F4528482B4D6251655468576D5A7134743777397A24432646294A404E635266556A586E3272357538782F4125442A472D==\n-----END PRIVATE KEY-----\n",
  "client_email": "image-registy-testing@openshift-test-project.iam.gserviceaccount.com",
  "client_id": "123456789101112131415",
  "auth_uri": "https://accounts.google.com/o/oauth2/auth",
  "token_uri": "https://oauth2.googleapis.com/token",
  "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
  "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/image-registy-testing%40openshift-test-project.iam.gserviceaccount.com"
}`
	fakeGCSCredsData = map[string]string{
		"REGISTRY_STORAGE_GCS_KEYFILE": fakeGCSKeyfile,
	}
)

// TestGCSMinimal is a test to verify that GCS credentials
// provided as part of the Day 2 experience will be propagated to the
// image-registry deployment
func TestGCSMinimal(t *testing.T) {
	client := framework.MustNewClientset(t, nil)

	defer framework.MustRemoveImageRegistry(t, client)

	// Custom resource configuration to use GCS
	cr := &imageregistryv1.Config{
		TypeMeta: metav1.TypeMeta{
			APIVersion: imageregistryv1.SchemeGroupVersion.String(),
			Kind:       "Config",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: imageregistryv1.ImageRegistryResourceName,
		},
		Spec: imageregistryv1.ImageRegistrySpec{
			ManagementState: operatorapi.Managed,
			Storage: imageregistryv1.ImageRegistryConfigStorage{
				GCS: &imageregistryv1.ImageRegistryConfigStorageGCS{
					Bucket: "openshift-test-bucket",
				},
			},
			Replicas: 1,
		},
	}

	// Create the image-registry-private-configuration-user secret using the invalid credentials
	err := wait.PollImmediate(1*time.Second, framework.AsyncOperationTimeout, func() (stop bool, err error) {
		if _, err := framework.CreateOrUpdateSecret(imageregistryv1.ImageRegistryPrivateConfigurationUser, imageregistryv1.ImageRegistryOperatorNamespace, fakeGCSCredsData); err != nil {
			t.Logf("unable to create secret: %s", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	framework.MustDeployImageRegistry(t, client, cr)
	framework.MustEnsureImageRegistryIsAvailable(t, client)
	framework.MustEnsureInternalRegistryHostnameIsSet(t, client)
	framework.MustEnsureClusterOperatorStatusIsSet(t, client)
	framework.MustEnsureOperatorIsNotHotLooping(t, client)

	// Check that the image-registry-private-configuration secret exists and
	// contains the correct information synced from the image-registry-private-configuration-user secret
	imageRegistryPrivateConfiguration, err := client.Secrets(imageregistryv1.ImageRegistryOperatorNamespace).Get(imageregistryv1.ImageRegistryPrivateConfiguration, metav1.GetOptions{})
	if err != nil {
		t.Errorf("unable to get secret %s/%s: %#v", imageregistryv1.ImageRegistryOperatorNamespace, imageregistryv1.ImageRegistryPrivateConfiguration, err)
	}
	keyfileData, _ := imageRegistryPrivateConfiguration.Data["REGISTRY_STORAGE_GCS_KEYFILE"]
	if string(keyfileData) != fakeGCSKeyfile {
		t.Errorf("secret %s/%s contains incorrect gcs credentials", imageregistryv1.ImageRegistryOperatorNamespace, imageregistryv1.ImageRegistryPrivateConfiguration)
	}

	registryDeployment, err := client.Deployments(imageregistryv1.ImageRegistryOperatorNamespace).Get(imageregistryv1.ImageRegistryName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Check that the GCS configuration environment variables
	// exist in the image registry deployment and
	// contain the correct values
	gcsEnvVars := []corev1.EnvVar{
		{Name: "REGISTRY_STORAGE", Value: "gcs", ValueFrom: nil},
		{Name: "REGISTRY_STORAGE_GCS_BUCKET", Value: string(cr.Spec.Storage.GCS.Bucket), ValueFrom: nil},
		{Name: "REGISTRY_STORAGE_GCS_KEYFILE", Value: "/gcs/keyfile", ValueFrom: nil},
	}

	for _, val := range gcsEnvVars {
		found := false
		for _, v := range registryDeployment.Spec.Template.Spec.Containers[0].Env {
			if v.Name == val.Name {
				found = true
				if !reflect.DeepEqual(v, val) {
					t.Errorf("environment variable contains incorrect data: expected %#v, got %#v", val, v)
				}
			}
		}
		if !found {
			t.Errorf("unable to find environment variable: wanted %s", val.Name)
		}
	}

	// Get a fresh version of the image registry resource
	cr, err = client.Configs().Get(imageregistryv1.ImageRegistryResourceName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("%s", err)
	}
}
