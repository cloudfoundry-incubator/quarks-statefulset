package quarksstatefulset

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	qstsv1a1 "code.cloudfoundry.org/quarks-statefulset/pkg/kube/apis/quarksstatefulset/v1alpha1"
	"code.cloudfoundry.org/quarks-statefulset/pkg/kube/controllers/statefulset"
	"code.cloudfoundry.org/quarks-statefulset/pkg/kube/util/mutate"
	"code.cloudfoundry.org/quarks-utils/pkg/config"
	"code.cloudfoundry.org/quarks-utils/pkg/ctxlog"
	"code.cloudfoundry.org/quarks-utils/pkg/meltdown"
	"code.cloudfoundry.org/quarks-utils/pkg/util"
	vss "code.cloudfoundry.org/quarks-utils/pkg/versionedsecretstore"
)

const (
	// EnvKubeAz is set by available zone name
	EnvKubeAz = "KUBE_AZ"
	// EnvBoshAz is set by available zone name
	EnvBoshAz = "BOSH_AZ"
	// EnvReplicas describes the number of replicas in the QuarksStatefulSet
	EnvReplicas = "REPLICAS"
	// EnvCfOperatorAz is set by available zone name
	EnvCfOperatorAz = "CF_OPERATOR_AZ"
	// EnvCFOperatorAZIndex is set by available zone index
	EnvCFOperatorAZIndex = "AZ_INDEX"
)

// Check that ReconcileQuarksStatefulSet implements the reconcile.Reconciler interface
var _ reconcile.Reconciler = &ReconcileQuarksStatefulSet{}

// ReconcileSkipDuration is the duration of merging consecutive triggers.
const ReconcileSkipDuration = 10 * time.Second

type setReferenceFunc func(owner, object metav1.Object, scheme *runtime.Scheme) error

// NewReconciler returns a new reconcile.Reconciler for QuarksStatefulSets
func NewReconciler(ctx context.Context, config *config.Config, mgr manager.Manager, srf setReferenceFunc, store vss.VersionedSecretStore) reconcile.Reconciler {
	return &ReconcileQuarksStatefulSet{
		ctx:                  ctx,
		config:               config,
		client:               mgr.GetClient(),
		scheme:               mgr.GetScheme(),
		setReference:         srf,
		versionedSecretStore: store,
	}
}

// ReconcileQuarksStatefulSet reconciles an QuarksStatefulSet object
type ReconcileQuarksStatefulSet struct {
	ctx                  context.Context
	client               client.Client
	scheme               *runtime.Scheme
	setReference         setReferenceFunc
	config               *config.Config
	versionedSecretStore vss.VersionedSecretStore
}

// Reconcile reads that state of the cluster for a QuarksStatefulSet object
// and makes changes based on the state read and what is in the QuarksStatefulSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileQuarksStatefulSet) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {

	// Fetch the QuarksStatefulSet we need to reconcile
	qStatefulSet := &qstsv1a1.QuarksStatefulSet{}

	// Set the ctx to be Background, as the top-level context for incoming requests.
	ctx, cancel := context.WithTimeout(r.ctx, r.config.CtxTimeOut)
	defer cancel()

	ctxlog.Info(ctx, "Reconciling QuarksStatefulSet ", request.NamespacedName)
	err := r.client.Get(ctx, request.NamespacedName, qStatefulSet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			ctxlog.Debug(ctx, "Skip QuarksStatefulSet reconcile: QuarksStatefulSet not found")
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Update labels of versioned secrets in quarksStatefulSet spec
	err = r.UpdateVersions(ctx, qStatefulSet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			ctxlog.Infof(ctx, "Requeue, waiting for secret: '%s'", err)
			return reconcile.Result{RequeueAfter: ReconcileSkipDuration}, nil
		}
		_ = ctxlog.WithEvent(qStatefulSet, "IncrementVersionError").Error(ctx, "Could not update labels of versioned secrets in QuarksStatefulSet '", request.NamespacedName, "': ", err)
		return reconcile.Result{}, err
	}

	if qStatefulSet.Status.LastReconcile == nil {
		now := metav1.Now()
		qStatefulSet.Status.LastReconcile = &now
		err = r.client.Status().Update(ctx, qStatefulSet)
		if err != nil {
			return reconcile.Result{},
				ctxlog.WithEvent(qStatefulSet, "UpdateError").Errorf(ctx, "failed to update reconcile timestamp on bdpl '%s' (%v): %s", request.NamespacedName, qStatefulSet.ResourceVersion, err)
		}
		ctxlog.Infof(ctx, "Meltdown started for '%s'", request.NamespacedName)

		return reconcile.Result{RequeueAfter: ReconcileSkipDuration}, nil
	}

	if meltdown.NewWindow(ReconcileSkipDuration, qStatefulSet.Status.LastReconcile).Contains(time.Now()) {
		ctxlog.Infof(ctx, "Meltdown in progress for '%s'", request.NamespacedName)
		return reconcile.Result{}, nil
	}
	ctxlog.Infof(ctx, "Meltdown ended for '%s'", request.NamespacedName)

	// Calculate the desired statefulSets
	desiredStatefulSets, err := r.calculateDesiredStatefulSets(ctx, qStatefulSet)
	if err != nil {
		return reconcile.Result{}, ctxlog.WithEvent(qStatefulSet, "CalculationError").Error(ctx, "Could not calculate StatefulSet owned by QuarksStatefulSet '", request.NamespacedName, "': ", err)
	}

	for _, desiredStatefulSet := range desiredStatefulSets {
		// If it doesn't exist, create it
		ctxlog.Infof(ctx, "StatefulSet '%s' owned by QuarksStatefulSet '%s' not found, will be created.",
			request.NamespacedName,
			desiredStatefulSet.Name)

		if err = r.versionedSecretStore.SetSecretReferences(ctx, request.Namespace, &qStatefulSet.Spec.Template.Spec.Template.Spec); err != nil {
			return reconcile.Result{}, ctxlog.WithEvent(qStatefulSet, "UpdateVersionedSecretReferencesError").Error(ctx, "Could not update versioned secret references in pod spec for QuarksStatefulSet '", request.NamespacedName, "': ", err)
		}
		if err := r.createStatefulSet(ctx, qStatefulSet, &desiredStatefulSet); err != nil {
			return reconcile.Result{}, ctxlog.WithEvent(qStatefulSet, "CreateStatefulSetError").Error(ctx, "Could not create StatefulSet for QuarksStatefulSet '", request.NamespacedName, "': ", err)
		}

		// Reset ready status if we create
		qStatefulSet.Status.Ready = false
	}

	return reconcile.Result{}, nil
}

// UpdateVersions updates the versions of all versioned secret
// mounted as volumes in QuarksStatefulSet
func (r *ReconcileQuarksStatefulSet) UpdateVersions(ctx context.Context, qStatefulSet *qstsv1a1.QuarksStatefulSet) error {

	secret := &corev1.Secret{}
	volumes := qStatefulSet.Spec.Template.Spec.Template.Spec.Volumes
	for volumeIndex, volume := range volumes {
		if volume.VolumeSource.Secret != nil {
			if err := r.client.Get(ctx, types.NamespacedName{Name: volume.Secret.SecretName, Namespace: qStatefulSet.Namespace}, secret); err != nil {
				return err
			}
			if vss.IsVersionedSecret(*secret) {
				secretNameSplitted := strings.Split(secret.GetName(), "-")
				latestSecret, err := r.versionedSecretStore.Latest(ctx, qStatefulSet.Namespace, strings.Join(secretNameSplitted[0:len(secretNameSplitted)-1], "-"))
				if err != nil {
					return errors.Wrapf(err, "failed to read latest versioned secret '%s' for QuarksStatefulSet '%s'", secret.GetName(), qStatefulSet.GetNamespacedName())
				}
				qStatefulSet.Spec.Template.Spec.Template.Spec.Volumes[volumeIndex].Secret.SecretName = latestSecret.GetName()
			}
		}
	}
	qStatefulSet.Spec.Template.Spec.Template.Spec.Volumes = volumes
	return nil
}

// calculateDesiredStatefulSets generates the desired StatefulSets that should exist
func (r *ReconcileQuarksStatefulSet) calculateDesiredStatefulSets(ctx context.Context, qStatefulSet *qstsv1a1.QuarksStatefulSet) ([]appsv1.StatefulSet, error) {
	var desiredStatefulSets []appsv1.StatefulSet

	template := qStatefulSet.Spec.Template.DeepCopy()

	// Place the StatefulSet in the same namespace as the QuarksStatefulSet
	template.SetNamespace(qStatefulSet.Namespace)

	// Set version
	// Get the current StatefulSet.
	_, currentVersion, err := GetMaxStatefulSetVersion(ctx, r.client, qStatefulSet)
	if err != nil {
		return nil, err
	}

	desiredVersion := currentVersion + 1
	ctxlog.Infof(ctx, "Creating new version '%d' for QuarksStatefulSet '%s'", desiredVersion, qStatefulSet.GetNamespacedName())

	if qStatefulSet.Spec.ZoneNodeLabel == "" {
		qStatefulSet.Spec.ZoneNodeLabel = qstsv1a1.DefaultZoneNodeLabel
	}

	if len(qStatefulSet.Spec.Zones) > 0 {
		for zoneIndex, zoneName := range qStatefulSet.Spec.Zones {
			statefulSet, err := r.generateSingleStatefulSet(qStatefulSet, template, zoneIndex, zoneName, desiredVersion)
			if err != nil {
				return desiredStatefulSets, errors.Wrapf(err, "Could not generate StatefulSet template for AZ '%d/%s'", zoneIndex, zoneName)
			}
			desiredStatefulSets = append(desiredStatefulSets, *statefulSet)
		}

	} else {
		statefulSet, err := r.generateSingleStatefulSet(qStatefulSet, template, 0, "", desiredVersion)
		if err != nil {
			return desiredStatefulSets, errors.Wrap(err, "Could not generate StatefulSet template for single zone")
		}
		desiredStatefulSets = append(desiredStatefulSets, *statefulSet)
	}

	return desiredStatefulSets, nil
}

// createStatefulSet creates a StatefulSet
func (r *ReconcileQuarksStatefulSet) createStatefulSet(ctx context.Context, qStatefulSet *qstsv1a1.QuarksStatefulSet, statefulSet *appsv1.StatefulSet) error {

	// Set the owner of the StatefulSet, so it's garbage collected,
	// and we can find it later
	ctxlog.Infof(ctx, "Setting owner for StatefulSet '%s' to QuarksStatefulSet '%s'", statefulSet.Name, qStatefulSet.GetNamespacedName())
	if err := r.setReference(qStatefulSet, statefulSet, r.scheme); err != nil {
		return errors.Wrapf(err, "could not set owner for StatefulSet '%s' to QuarksStatefulSet '%s'", statefulSet.Name, qStatefulSet.GetNamespacedName())
	}

	// Create or update the StatefulSet
	if _, err := controllerutil.CreateOrUpdate(ctx, r.client, statefulSet, mutate.StatefulSetMutateFn(statefulSet)); err != nil {
		return errors.Wrapf(err, "could not create or update StatefulSet '%s' for QuarksStatefulSet '%s'", statefulSet.Name, qStatefulSet.GetNamespacedName())
	}
	ctxlog.Infof(ctx, "Created/Updated StatefulSet '%s' for QuarksStatefulSet '%s'", statefulSet.Name, qStatefulSet.GetNamespacedName())
	return nil
}

// generateSingleStatefulSet creates a StatefulSet from one zone
func (r *ReconcileQuarksStatefulSet) generateSingleStatefulSet(qStatefulSet *qstsv1a1.QuarksStatefulSet, template *appsv1.StatefulSet, zoneIndex int, zoneName string, version int) (*appsv1.StatefulSet, error) {
	statefulSet := template.DeepCopy()

	statefulSetNamePrefix := qStatefulSet.GetName()
	labels := make(map[string]string)
	annotations := make(map[string]string)

	// Update available-zone specified properties
	if zoneName != "" {
		// Override name prefix with zoneIndex
		statefulSetNamePrefix = fmt.Sprintf("%s-%s", qStatefulSet.GetName(), zoneName)

		labels[qstsv1a1.LabelAZName] = zoneName

		zonesBytes, err := json.Marshal(qStatefulSet.Spec.Zones)
		if err != nil {
			return &appsv1.StatefulSet{}, errors.Wrapf(err, "Could not marshal zones: '%v'", qStatefulSet.Spec.Zones)
		}
		annotations[qstsv1a1.AnnotationZones] = string(zonesBytes)

		statefulSet = r.updateAffinity(statefulSet, qStatefulSet.Spec.ZoneNodeLabel, zoneName)
	}
	labels[qstsv1a1.LabelAZIndex] = strconv.Itoa(zoneIndex)
	labels[qstsv1a1.LabelQStsName] = statefulSetNamePrefix

	annotations[statefulset.AnnotationCanaryRolloutEnabled] = "true"

	// Set updated properties
	statefulSet.Spec.Template.SetLabels(util.UnionMaps(statefulSet.Spec.Template.GetLabels(), labels))
	statefulSet.Spec.Template.SetAnnotations(util.UnionMaps(statefulSet.Spec.Template.GetAnnotations(), annotations))
	statefulSet.SetName(statefulSetNamePrefix)
	statefulSet.SetLabels(util.UnionMaps(statefulSet.GetLabels(), labels))
	// Spec.Selector has to match Spec.Template.Labels
	statefulSet.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels,
	}

	annotations[qstsv1a1.AnnotationVersion] = strconv.Itoa(version)
	statefulSet.SetAnnotations(util.UnionMaps(statefulSet.GetAnnotations(), annotations))

	r.injectContainerEnv(&statefulSet.Spec.Template.Spec, zoneIndex, zoneName, qStatefulSet.Spec.Template.Spec.Replicas, qStatefulSet.Spec.InjectReplicasEnv)
	return statefulSet, nil
}

// updateAffinity Update current statefulSet Affinity from AZ specification
func (r *ReconcileQuarksStatefulSet) updateAffinity(statefulSet *appsv1.StatefulSet, zoneNodeLabel string, zoneName string) *appsv1.StatefulSet {
	nodeInZoneSelector := corev1.NodeSelectorRequirement{
		Key:      zoneNodeLabel,
		Operator: corev1.NodeSelectorOpIn,
		Values:   []string{zoneName},
	}

	affinity := statefulSet.Spec.Template.Spec.Affinity
	// Check if optional properties were set
	if affinity == nil {
		affinity = &corev1.Affinity{}
	}

	if affinity.NodeAffinity == nil {
		affinity.NodeAffinity = &corev1.NodeAffinity{}
	}

	if affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						nodeInZoneSelector,
					},
				},
			},
		}
	} else {
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, corev1.NodeSelectorTerm{
			MatchExpressions: []corev1.NodeSelectorRequirement{
				nodeInZoneSelector,
			},
		})
	}

	statefulSet.Spec.Template.Spec.Affinity = affinity

	return statefulSet
}

// injectContainerEnv inject AZ info to container envs
func (r *ReconcileQuarksStatefulSet) injectContainerEnv(podSpec *corev1.PodSpec, zoneIndex int, zoneName string, replicas *int32, injectReplicasEnv *bool) {

	containers := []*corev1.Container{}
	for i := 0; i < len(podSpec.Containers); i++ {
		containers = append(containers, &podSpec.Containers[i])
	}
	for i := 0; i < len(podSpec.InitContainers); i++ {
		containers = append(containers, &podSpec.InitContainers[i])
	}
	for _, container := range containers {
		envs := container.Env

		if zoneIndex >= 0 {
			envs = upsertEnvs(envs, EnvKubeAz, zoneName)
			envs = upsertEnvs(envs, EnvBoshAz, zoneName)
			envs = upsertEnvs(envs, EnvCfOperatorAz, zoneName)
			envs = upsertEnvs(envs, EnvCFOperatorAZIndex, strconv.Itoa(zoneIndex+1))
		} else {
			// Default to zone 1
			envs = upsertEnvs(envs, EnvCFOperatorAZIndex, "1")
		}

		if (injectReplicasEnv == nil) || (*injectReplicasEnv) {
			envs = upsertEnvs(envs, EnvReplicas, strconv.Itoa(int(*replicas)))
		}

		container.Env = envs
	}
}

func upsertEnvs(envs []corev1.EnvVar, name string, value string) []corev1.EnvVar {
	for idx, env := range envs {
		if env.Name == name {
			envs[idx].Value = value
			return envs
		}
	}

	envs = append(envs, corev1.EnvVar{
		Name:  name,
		Value: value,
	})
	return envs
}
