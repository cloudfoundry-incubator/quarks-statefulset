package manifest

import (
	"fmt"
	"path/filepath"

	bdv1 "code.cloudfoundry.org/cf-operator/pkg/kube/apis/boshdeployment/v1alpha1"
	ejv1 "code.cloudfoundry.org/cf-operator/pkg/kube/apis/extendedjob/v1alpha1"
	"code.cloudfoundry.org/cf-operator/pkg/kube/util/names"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobFactory creates Jobs for a given manifest
type JobFactory struct {
	Manifest  Manifest
	Namespace string
}

// NewJobFactory returns a new JobFactory
func NewJobFactory(manifest Manifest, namespace string) *JobFactory {
	return &JobFactory{Manifest: manifest, Namespace: namespace}
}

// VariableInterpolationJob returns an extended job to interpolate variables
func (f *JobFactory) VariableInterpolationJob() (*ejv1.ExtendedJob, error) {
	cmd := []string{"/bin/sh"}
	args := []string{"-c", `cf-operator util variable-interpolation`}

	// This is the source manifest, that still has the '((vars))'
	manifestSecretName := names.CalculateSecretName(names.DeploymentSecretTypeManifestWithOps, f.Manifest.Name, "")

	// Prepare Volumes and Volume mounts

	// This is a volume for the "not interpolated" manifest,
	// that has the ops files applied, but still contains '((vars))'
	volumes := []corev1.Volume{
		{
			Name: generateVolumeName(manifestSecretName),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: manifestSecretName,
				},
			},
		},
	}
	// Volume mount for the manifest
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      generateVolumeName(manifestSecretName),
			MountPath: "/var/run/secrets/deployment/",
			ReadOnly:  true,
		},
	}

	// We need a volume and a mount for each input variable
	for _, variable := range f.Manifest.Variables {
		varName := variable.Name
		varSecretName := names.CalculateSecretName(names.DeploymentSecretTypeGeneratedVariable, f.Manifest.Name, varName)

		// The volume definition
		vol := corev1.Volume{
			Name: generateVolumeName(varSecretName),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: varSecretName,
				},
			},
		}
		volumes = append(volumes, vol)

		// And the volume mount
		volMount := corev1.VolumeMount{
			Name:      generateVolumeName(varSecretName),
			MountPath: "/var/run/secrets/variables/" + varName,
			ReadOnly:  true,
		}
		volumeMounts = append(volumeMounts, volMount)
	}

	// If there are no variables, mount an empty dir for variables
	if len(f.Manifest.Variables) == 0 {
		// The volume definition
		vol := corev1.Volume{
			Name: generateVolumeName("no-vars"),
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
		volumes = append(volumes, vol)

		// And the volume mount
		volMount := corev1.VolumeMount{
			Name:      generateVolumeName("no-vars"),
			MountPath: "/var/run/secrets/variables/",
			ReadOnly:  true,
		}
		volumeMounts = append(volumeMounts, volMount)
	}

	// Calculate the signature of the manifest, to label things
	manifestSignature, err := f.Manifest.SHA1()
	if err != nil {
		return nil, errors.Wrap(err, "could not calculate manifest SHA1")
	}

	outputSecretPrefix, _ := names.CalculateEJobOutputSecretPrefixAndName(
		names.DeploymentSecretTypeManifestAndVars,
		f.Manifest.Name,
		VarInterpolationContainerName,
		false,
	)

	eJobName := fmt.Sprintf("var-interpolation-%s", f.Manifest.Name)

	// Construct the var interpolation job
	job := &ejv1.ExtendedJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eJobName,
			Namespace: f.Namespace,
			Labels: map[string]string{
				bdv1.LabelDeploymentName: f.Manifest.Name,
			},
		},
		Spec: ejv1.ExtendedJobSpec{
			UpdateOnConfigChange: true,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: eJobName,
					Labels: map[string]string{
						"delete": "pod",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:         VarInterpolationContainerName,
							Image:        GetOperatorDockerImage(),
							Command:      cmd,
							Args:         args,
							VolumeMounts: volumeMounts,
							Env: []corev1.EnvVar{
								{
									Name:  "BOSH_MANIFEST_PATH",
									Value: filepath.Join("/var/run/secrets/deployment/", DesiredManifestKeyName),
								},
								{
									Name:  "VARIABLES_DIR",
									Value: "/var/run/secrets/variables/",
								},
							},
						},
					},
					Volumes: volumes,
				},
			},
			Output: &ejv1.Output{
				NamePrefix: outputSecretPrefix,
				SecretLabels: map[string]string{
					bdv1.LabelDeploymentName:    f.Manifest.Name,
					bdv1.LabelManifestSHA1:      manifestSignature,
					ejv1.LabelReferencedJobName: fmt.Sprintf("data-gathering-%s", f.Manifest.Name),
				},
				Versioned: true,
			},
			Trigger: ejv1.Trigger{
				Strategy: ejv1.TriggerOnce,
			},
		},
	}
	return job, nil
}

// dataGatheringJob generates the Data Gathering Job for a manifest
func (f *JobFactory) DataGatheringJob() (*ejv1.ExtendedJob, error) {

	_, interpolatedManifestSecretName := names.CalculateEJobOutputSecretPrefixAndName(
		names.DeploymentSecretTypeManifestAndVars,
		f.Manifest.Name,
		VarInterpolationContainerName,
		true,
	)

	eJobName := fmt.Sprintf("data-gathering-%s", f.Manifest.Name)
	outputSecretNamePrefix, _ := names.CalculateEJobOutputSecretPrefixAndName(
		names.DeploymentSecretTypeInstanceGroupResolvedProperties,
		f.Manifest.Name,
		"",
		false,
	)

	initContainers := []corev1.Container{}
	containers := make([]corev1.Container, len(f.Manifest.InstanceGroups))

	doneSpecCopyingReleases := map[string]bool{}

	for idx, ig := range f.Manifest.InstanceGroups {

		// Iterate through each Job to find all releases so we can copy all
		// sources to /var/vcap/data-gathering
		for _, boshJob := range ig.Jobs {
			// If we've already generated an init container for this release, skip
			releaseName := boshJob.Release
			if _, ok := doneSpecCopyingReleases[releaseName]; ok {
				continue
			}
			doneSpecCopyingReleases[releaseName] = true

			// Get the docker image for the release
			releaseImage, err := f.Manifest.GetReleaseImage(ig.Name, boshJob.Name)
			if err != nil {
				return nil, errors.Wrap(err, "failed to calculate release image for data gathering")
			}
			// Create an init container that copies sources
			// TODO: destination should also contain release name, to prevent overwrites
			initContainers = append(initContainers, jobSpecCopierContainer(releaseName, releaseImage, generateVolumeName("data-gathering")))
		}

		// One container per Instance Group
		// There will be one secret generated for each of these containers
		containers[idx] = corev1.Container{
			Name:    ig.Name,
			Image:   GetOperatorDockerImage(),
			Command: []string{"/bin/sh"},
			Args:    []string{"-c", `cf-operator util data-gather`},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      generateVolumeName(interpolatedManifestSecretName),
					MountPath: "/var/run/secrets/deployment/",
					ReadOnly:  true,
				},
				{
					Name:      generateVolumeName("data-gathering"),
					MountPath: "/var/vcap/all-releases",
				},
			},
			Env: []corev1.EnvVar{
				{
					Name:  "BOSH_MANIFEST_PATH",
					Value: filepath.Join("/var/run/secrets/deployment/", DesiredManifestKeyName),
				},
				{
					Name:  "CF_OPERATOR_NAMESPACE",
					Value: f.Namespace,
				},
				{
					Name:  "BASE_DIR",
					Value: "/var/vcap/all-releases",
				},
				{
					Name:  "INSTANCE_GROUP_NAME",
					Value: ig.Name,
				},
			},
		}
	}

	// Construct the data gathering job
	dataGatheringJob := &ejv1.ExtendedJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eJobName,
			Namespace: f.Namespace,
		},
		Spec: ejv1.ExtendedJobSpec{
			Output: &ejv1.Output{
				NamePrefix: outputSecretNamePrefix,
				SecretLabels: map[string]string{
					bdv1.LabelDeploymentName: f.Manifest.Name,
				},
				Versioned: true,
			},
			Trigger: ejv1.Trigger{
				Strategy: ejv1.TriggerOnce,
			},
			UpdateOnConfigChange: true,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: eJobName,
					Labels: map[string]string{
						"delete": "pod",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					// Init Container to copy contents
					InitContainers: initContainers,
					// Container to run data gathering
					Containers: containers,
					// Volumes for secrets
					Volumes: []corev1.Volume{
						{
							Name: generateVolumeName(interpolatedManifestSecretName),
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: interpolatedManifestSecretName,
								},
							},
						},
						{
							Name: generateVolumeName("data-gathering"),
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	return dataGatheringJob, nil
}