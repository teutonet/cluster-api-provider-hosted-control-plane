/*
Copyright 2022 teuto.net Netzdienste GmbH.
*/

package v1alpha1

import (
	"context"
	"fmt"

	semver "github.com/blang/semver/v4"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/cluster-api/util/version"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (c *HostedControlPlane) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return errorsUtil.IfErrErrorf("failed to setup webhook: %w", ctrl.NewWebhookManagedBy(mgr).
		For(c).
		WithValidator(&hostedControlPlaneWebhook{}).
		Complete())
}

//+kubebuilder:webhook:path=/validate-hcp-teuto-net-v1alpha1-hostedcontrolplane,mutating=false,failurePolicy=fail,sideEffects=None,groups=hcp.teuto.net,resources=hostedcontrolplanes,verbs=create;update,versions=v1alpha1,name=vhostedcontrolplane.kb.io,admissionReviewVersions=v1
//+kubebuilder:metadata:annotations="cert-manager.io/inject-ca-from=cluster-api-control-plane-provider-hosted-control-plane-webhook-ca"

type hostedControlPlaneWebhook struct {
	groupKind schema.GroupKind
	specPath  *field.Path
}

var _ webhook.CustomValidator = &hostedControlPlaneWebhook{
	groupKind: SchemeGroupVersion.WithKind("HostedControlPlane").GroupKind(),
	specPath:  field.NewPath("spec"),
}

func (w *hostedControlPlaneWebhook) ValidateCreate(
	_ context.Context,
	newObj runtime.Object,
) (admission.Warnings, error) {
	newHostedControlPlane, err := w.castObjectToHostedControlPlane(newObj)
	if err != nil {
		return []string{}, err
	}

	if _, fieldErr := w.parseVersion(newHostedControlPlane); fieldErr != nil {
		return []string{}, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, field.ErrorList{fieldErr})
	}

	return []string{}, nil
}

func (w *hostedControlPlaneWebhook) castObjectToHostedControlPlane(
	obj runtime.Object,
) (*HostedControlPlane, *apierrors.StatusError) {
	hostedControlPlane, ok := obj.(*HostedControlPlane)
	if !ok {
		return nil, apierrors.NewBadRequest("expected a HostedControlPlane but got wrong type")
	}
	return hostedControlPlane, nil
}

func (w *hostedControlPlaneWebhook) parseVersion(
	hostedControlPlane *HostedControlPlane,
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
	_ context.Context,
	oldObj runtime.Object,
	newObj runtime.Object,
) (admission.Warnings, error) {
	newHostedControlPlane, err := w.castObjectToHostedControlPlane(newObj)
	if err != nil {
		return []string{}, err
	}
	oldHostedControlPlane, err := w.castObjectToHostedControlPlane(oldObj)
	if err != nil {
		return []string{}, err
	}

	oldVersion, fieldErr := w.parseVersion(oldHostedControlPlane)
	if fieldErr != nil {
		return []string{}, apierrors.NewInvalid(w.groupKind, newHostedControlPlane.Name, field.ErrorList{fieldErr})
	}

	// ignore fieldErr because we're going to validate the whole object anyway.
	newVersion, _ := w.parseVersion(newHostedControlPlane)
	warnings, objErr := w.ValidateCreate(context.Background(), newHostedControlPlane)
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

	if newHostedControlPlane.Spec.ETCD.VolumeSize.Cmp(oldHostedControlPlane.Spec.ETCD.VolumeSize) == -1 {
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

	return warnings, nil
}

func (*hostedControlPlaneWebhook) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return []string{}, nil
}
