/*
Copyright 2022 teuto.net Netzdienste GmbH.
*/

package webhook

import (
	"context"
	"fmt"

	semver "github.com/blang/semver/v4"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/importcycle"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/kubeconfig"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/version"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return errorsUtil.IfErrErrorf("failed to setup webhook: %w",
		ctrl.NewWebhookManagedBy(mgr, &v1alpha1.HostedControlPlane{}).
			WithValidator(NewHostedControlPlaneWebhook(mgr.GetClient())).
			Complete(),
	)
}

//+kubebuilder:webhook:path=/validate-controlplane-cluster-x-k8s-io-v1alpha1-hostedcontrolplane,mutating=false,failurePolicy=fail,sideEffects=None,groups=controlplane.cluster.x-k8s.io,resources=hostedcontrolplanes,verbs=create;update,versions=v1alpha1,name=vhostedcontrolplane.kb.io,admissionReviewVersions=v1,serviceName=controller-manager

type hostedControlPlaneWebhook struct {
	client    client.Client
	groupKind schema.GroupKind
	specPath  *field.Path
}

func NewHostedControlPlaneWebhook(
	client client.Client,
) *hostedControlPlaneWebhook {
	return &hostedControlPlaneWebhook{
		client:    client,
		groupKind: schema.GroupKind{Group: "controlplane.cluster.x-k8s.io", Kind: "HostedControlPlane"},
		specPath:  field.NewPath("spec"),
	}
}

var _ admission.Validator[*v1alpha1.HostedControlPlane] = &hostedControlPlaneWebhook{}

func (w *hostedControlPlaneWebhook) ValidateCreate(
	ctx context.Context,
	newHostedControlPlane *v1alpha1.HostedControlPlane,
) (admission.Warnings, error) {
	fieldErrs := field.ErrorList{}
	warnings := admission.Warnings{}

	if _, fieldErr := w.parseVersion(newHostedControlPlane); fieldErr != nil {
		fieldErrs = append(fieldErrs, fieldErr)
	}

	if newHostedControlPlane.Spec.ETCD.AutoGrowEnabled() &&
		newHostedControlPlane.Spec.ETCD.VolumeSize != nil &&
		!newHostedControlPlane.Spec.ETCD.VolumeSize.IsZero() {
		fieldErrs = append(fieldErrs, field.Invalid(
			w.specPath.Child("etcd").Child("autoGrow"),
			newHostedControlPlane.Spec.ETCD.AutoGrow,
			"autoGrow cannot be true when volumeSize is set",
		))
	}

	if !newHostedControlPlane.Spec.ETCD.AutoGrowEnabled() &&
		(newHostedControlPlane.Spec.ETCD.VolumeSize == nil ||
			newHostedControlPlane.Spec.ETCD.VolumeSize.IsZero()) {
		fieldErrs = append(fieldErrs, field.Invalid(
			w.specPath.Child("etcd").Child("autoGrow"),
			newHostedControlPlane.Spec.ETCD.AutoGrow,
			"autoGrow cannot be false when volumeSize is not set",
		))
	}

	if len(newHostedControlPlane.Spec.CustomKubeconfigs) > 0 {
		cluster, err := util.GetOwnerCluster(ctx, w.client, newHostedControlPlane.ObjectMeta)
		switch {
		case err != nil:
			return nil, apierrors.NewInternalError(fmt.Errorf("failed to get owner cluster: %w", err))
		case cluster == nil:
			warnings = append(
				warnings,
				"unable to validate custom kubeconfig names because owner cluster could not be determined",
			)
		default:
			apiEndpoint := capiv2.APIEndpoint{}
			kubeconfigUsernames := slices.Keys(kubeconfig.CreateBuiltinKubeconfigConfigs(
				cluster,
				apiEndpoint, apiEndpoint, apiEndpoint,
				importcycle.KonnectivityClientUsername, importcycle.ControllerUsername,
			))
			fieldErrs = append(fieldErrs, slices.Flatten(slices.FilterMapToSlice(
				newHostedControlPlane.Spec.CustomKubeconfigs,
				func(name string, kubeconfig v1alpha1.KubeconfigEndpointType) ([]*field.Error, bool) {
					nameFieldPath := w.specPath.Child("customKubeconfigs").Key(name)
					if slices.Contains(kubeconfigUsernames, name) {
						return []*field.Error{field.Invalid(
							nameFieldPath, name,
							"custom kubeconfig username cannot be the same as a default kubeconfig username",
						)}, true
					}
					if errs := validation.NameIsDNSSubdomain(name, false); len(errs) > 0 {
						return slices.Map(errs, func(err string, _ int) *field.Error {
							return field.Invalid(
								nameFieldPath, name,
								fmt.Sprintf("custom kubeconfig name must be a valid DNS subdomain: %s", err),
							)
						}), true
					}
					// in combination with cluster info the resulting secret might be too long.
					if errs := validation.NameIsDNSSubdomain(
						names.GetCustomKubeconfigSecretName(cluster, name), false,
					); len(errs) > 0 {
						return slices.Map(errs, func(err string, _ int) *field.Error {
							return field.Invalid(
								nameFieldPath, name,
								fmt.Sprintf("custom kubeconfig name would result in an invalid secret name: %s", err),
							)
						}), true
					}
					return nil, false
				},
			))...)
		}
	}

	if len(fieldErrs) > 0 {
		return warnings, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, fieldErrs)
	}

	return warnings, nil
}

func (w *hostedControlPlaneWebhook) parseVersion(
	hostedControlPlane *v1alpha1.HostedControlPlane,
) (*semver.Version, *field.Error) {
	controlPlaneVersion, err := semver.ParseTolerant(hostedControlPlane.Spec.Version)
	if err != nil {
		return nil, field.Invalid(
			w.specPath.Child("version"),
			hostedControlPlane.Spec.Version,
			"version must be a valid semantic version",
		)
	}
	return &controlPlaneVersion, nil
}

func (w *hostedControlPlaneWebhook) ValidateUpdate(
	ctx context.Context,
	oldHostedControlPlane *v1alpha1.HostedControlPlane,
	newHostedControlPlane *v1alpha1.HostedControlPlane,
) (admission.Warnings, error) {
	oldVersion, fieldErr := w.parseVersion(oldHostedControlPlane)
	if fieldErr != nil {
		return nil, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, field.ErrorList{fieldErr})
	}

	// ignore fieldErr because we're going to validate the whole object anyway.
	newVersion, _ := w.parseVersion(newHostedControlPlane)
	warnings, objErr := w.ValidateCreate(ctx, newHostedControlPlane)
	if objErr != nil {
		return warnings, objErr
	}

	if version.Compare(*newVersion, *oldVersion, version.WithBuildTags()) == -1 {
		return warnings, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, field.ErrorList{field.Invalid(
			w.specPath.Child("version"),
			newHostedControlPlane.Spec.Version,
			fmt.Sprintf("version cannot be decreased from %q to %q", oldVersion, newVersion),
		)})
	}

	if !newHostedControlPlane.Spec.ETCD.AutoGrowEnabled() &&
		newHostedControlPlane.Spec.ETCD.VolumeSize != nil {
		if oldHostedControlPlane.Spec.ETCD.AutoGrowEnabled() &&
			newHostedControlPlane.Spec.ETCD.VolumeSize.Cmp(oldHostedControlPlane.Status.ETCDVolumeSize) == -1 {
			return warnings, apierrors.NewInvalid(
				w.groupKind,
				newHostedControlPlane.Name,
				field.ErrorList{field.Invalid(
					w.specPath.Child("etcd").Child("volumeSize"),
					newHostedControlPlane.Spec.ETCD.VolumeSize,
					fmt.Sprintf(
						"volume size cannot be decreased from %v to %v",
						oldHostedControlPlane.Status.ETCDVolumeSize,
						newHostedControlPlane.Spec.ETCD.VolumeSize,
					),
				)},
			)
		} else if oldHostedControlPlane.Spec.ETCD.VolumeSize != nil &&
			newHostedControlPlane.Spec.ETCD.VolumeSize.Cmp(*oldHostedControlPlane.Spec.ETCD.VolumeSize) == -1 {
			return warnings, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, field.ErrorList{field.Invalid(
				w.specPath.Child("etcd").Child("volumeSize"),
				newHostedControlPlane.Spec.ETCD.VolumeSize,
				fmt.Sprintf(
					"volume size cannot be decreased from %v to %v",
					oldHostedControlPlane.Spec.ETCD.VolumeSize,
					newHostedControlPlane.Spec.ETCD.VolumeSize,
				),
			)})
		}
	}

	return warnings, nil
}

func (*hostedControlPlaneWebhook) ValidateDelete(
	_ context.Context,
	_ *v1alpha1.HostedControlPlane,
) (admission.Warnings, error) {
	return nil, nil
}
