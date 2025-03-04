package swift

import (
	"math/rand"
	"reflect"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/objectstorage/v1/containers"

	corev1 "k8s.io/api/core/v1"

	operatorapi "github.com/openshift/api/operator/v1"
	imageregistryv1 "github.com/openshift/cluster-image-registry-operator/pkg/apis/imageregistry/v1"
	regopclient "github.com/openshift/cluster-image-registry-operator/pkg/client"
	"github.com/openshift/cluster-image-registry-operator/pkg/clusterconfig"
	"github.com/openshift/cluster-image-registry-operator/pkg/storage/util"
)

type driver struct {
	// Config is a struct where the basic configuration is stored
	Config *imageregistryv1.ImageRegistryConfigStorageSwift
	// Listers are used to download OpenStack credentials from the native secret
	Listers *regopclient.Listers
}

// replaceEmpty is a helper function to replace empty fields with another field
func replaceEmpty(a string, b string) string {
	if a == "" {
		return b
	}
	return a
}

// getSwiftClient returns a client that allows to interact with the OpenStack Swift service
func (d *driver) getSwiftClient(cr *imageregistryv1.Config) (*gophercloud.ServiceClient, error) {
	cfg, err := clusterconfig.GetSwiftConfig(d.Listers)
	if err != nil {
		return nil, err
	}

	d.Config.AuthURL = replaceEmpty(d.Config.AuthURL, cfg.Storage.Swift.AuthURL)
	d.Config.Tenant = replaceEmpty(d.Config.Tenant, cfg.Storage.Swift.Tenant)
	d.Config.TenantID = replaceEmpty(d.Config.TenantID, cfg.Storage.Swift.TenantID)
	d.Config.Domain = replaceEmpty(d.Config.Domain, cfg.Storage.Swift.Domain)
	d.Config.DomainID = replaceEmpty(d.Config.DomainID, cfg.Storage.Swift.DomainID)
	d.Config.RegionName = replaceEmpty(d.Config.RegionName, cfg.Storage.Swift.RegionName)

	opts := &gophercloud.AuthOptions{
		IdentityEndpoint: d.Config.AuthURL,
		Username:         cfg.Storage.Swift.Username,
		Password:         cfg.Storage.Swift.Password,
		DomainID:         d.Config.DomainID,
		DomainName:       d.Config.Domain,
		TenantID:         d.Config.TenantID,
		TenantName:       d.Config.Tenant,
	}

	provider, err := openstack.AuthenticatedClient(*opts)
	if err != nil {
		return nil, err
	}

	endpointOpts := gophercloud.EndpointOpts{
		Region: d.Config.RegionName,
		Name:   "swift",
	}

	var client *gophercloud.ServiceClient
	client, err = openstack.NewContainerV1(provider, endpointOpts)
	if err != nil {
		if _, ok := err.(*gophercloud.ErrEndpointNotFound); ok {
			endpointOpts.Type = "object-store"
			client, err = openstack.NewContainerV1(provider, endpointOpts)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	return client, nil
}

// NewDriver creates new Swift driver for the Image Registry
func NewDriver(c *imageregistryv1.ImageRegistryConfigStorageSwift, listers *regopclient.Listers) *driver {
	return &driver{
		Config:  c,
		Listers: listers,
	}
}

// Secrets returns a map of the storage access secrets
func (d *driver) Secrets() (map[string]string, error) {
	cfg, err := clusterconfig.GetSwiftConfig(d.Listers)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"REGISTRY_STORAGE_SWIFT_USERNAME": cfg.Storage.Swift.Username,
		"REGISTRY_STORAGE_SWIFT_PASSWORD": cfg.Storage.Swift.Password,
	}, nil
}

func (d *driver) ConfigEnv() (envs []corev1.EnvVar, err error) {
	cfg, err := clusterconfig.GetSwiftConfig(d.Listers)
	if err != nil {
		return nil, err
	}

	d.Config.AuthURL = replaceEmpty(d.Config.AuthURL, cfg.Storage.Swift.AuthURL)
	d.Config.Tenant = replaceEmpty(d.Config.Tenant, cfg.Storage.Swift.Tenant)
	d.Config.TenantID = replaceEmpty(d.Config.TenantID, cfg.Storage.Swift.TenantID)
	d.Config.Domain = replaceEmpty(d.Config.Domain, cfg.Storage.Swift.Domain)
	d.Config.DomainID = replaceEmpty(d.Config.DomainID, cfg.Storage.Swift.DomainID)
	d.Config.RegionName = replaceEmpty(d.Config.RegionName, cfg.Storage.Swift.RegionName)

	envs = append(envs,
		corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: "swift"},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_CONTAINER", Value: d.Config.Container},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_AUTHURL", Value: d.Config.AuthURL},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_DOMAIN", Value: d.Config.Domain},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_DOMAINID", Value: d.Config.DomainID},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_TENANT", Value: d.Config.Tenant},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_TENANTID", Value: d.Config.TenantID},
		corev1.EnvVar{
			Name: "REGISTRY_STORAGE_SWIFT_USERNAME",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: imageregistryv1.ImageRegistryPrivateConfiguration,
					},
					Key: "REGISTRY_STORAGE_SWIFT_USERNAME",
				},
			},
		},
		corev1.EnvVar{
			Name: "REGISTRY_STORAGE_SWIFT_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: imageregistryv1.ImageRegistryPrivateConfiguration,
					},
					Key: "REGISTRY_STORAGE_SWIFT_PASSWORD",
				},
			},
		},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_REGION", Value: d.Config.RegionName},
	)

	return
}

func (d *driver) StorageExists(cr *imageregistryv1.Config) (bool, error) {
	client, err := d.getSwiftClient(cr)
	if err != nil {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionUnknown, "Could not connect to registry storage", err.Error())
		return false, err
	}

	_, err = containers.Get(client, cr.Spec.Storage.Swift.Container, containers.GetOpts{}).Extract()
	if err != nil {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionFalse, "Storage does not exist", err.Error())
		return false, err
	}

	util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionTrue, "Swift container Exists", "")
	return true, nil
}

func (d *driver) StorageChanged(cr *imageregistryv1.Config) bool {
	if !reflect.DeepEqual(cr.Status.Storage.Swift, cr.Spec.Storage.Swift) {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionUnknown, "Swift Configuration Changed", "Swift storage is in an unknown state")
		return true
	}

	return false
}

func generateContainerName(prefix string) string {
	bytes := make([]byte, 16)
	for i := 0; i < 16; i++ {
		bytes[i] = byte(65 + rand.Intn(25)) // A=65 and Z=65+25
	}
	return prefix + string(bytes)
}

func (d *driver) CreateStorage(cr *imageregistryv1.Config) error {
	client, err := d.getSwiftClient(cr)
	if err != nil {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionFalse, err.Error(), err.Error())
		return err
	}

	// Generate new container name if it wasn't provided.
	// The name has a prefix "image_registry_", which is complemented by 16 capital latin letters
	// Example of a generated name: image_registry_FHEIBGDDGBLWPXFR
	if cr.Spec.Storage.Swift.Container == "" {
		cr.Spec.Storage.Swift.Container = generateContainerName("image_registry_")
	}

	_, err = containers.Create(client, cr.Spec.Storage.Swift.Container, containers.CreateOpts{}).Extract()
	if err != nil {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionFalse, "Creation Failed", err.Error())
		cr.Status.StorageManaged = false
		return err
	}

	util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionTrue, "Swift Container Created", "")

	cr.Status.StorageManaged = true
	cr.Status.Storage.Swift = d.Config.DeepCopy()
	cr.Spec.Storage.Swift = d.Config.DeepCopy()

	return nil
}

func (d *driver) RemoveStorage(cr *imageregistryv1.Config) (bool, error) {
	if !cr.Status.StorageManaged {
		return false, nil
	}

	client, err := d.getSwiftClient(cr)
	if err != nil {
		return false, err
	}

	_, err = containers.Delete(client, cr.Spec.Storage.Swift.Container).Extract()
	if err != nil {
		util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionUnknown, err.Error(), err.Error())
		return false, err
	}

	cr.Spec.Storage.Swift.Container = ""
	d.Config.Container = ""

	if !reflect.DeepEqual(cr.Status.Storage.Swift, d.Config) {
		cr.Status.Storage.Swift = d.Config.DeepCopy()
	}

	util.UpdateCondition(cr, imageregistryv1.StorageExists, operatorapi.ConditionFalse, "Swift Container Deleted", "The swift container has been removed.")

	return true, nil
}

func (d *driver) Volumes() ([]corev1.Volume, []corev1.VolumeMount, error) {
	return nil, nil, nil
}
