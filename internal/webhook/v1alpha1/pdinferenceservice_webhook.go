/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

// nolint:unused
var pdinferenceservicelog = logf.Log.WithName("pdinferenceservice-resource")

// SetupPDInferenceServiceWebhookWithManager registers the webhook for PDInferenceService in the manager.
func SetupPDInferenceServiceWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&pdaiv1alpha1.PDInferenceService{}).
		WithValidator(&PDInferenceServiceCustomValidator{Client: mgr.GetClient()}).
		WithDefaulter(&PDInferenceServiceCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-pdai-pdai-io-v1alpha1-pdinferenceservice,mutating=true,failurePolicy=fail,sideEffects=None,groups=pdai.pdai.io,resources=pdinferenceservices,verbs=create;update,versions=v1alpha1,name=mpdinferenceservice-v1alpha1.kb.io,admissionReviewVersions=v1

// PDInferenceServiceCustomDefaulter injects default values into PDInferenceService objects.
type PDInferenceServiceCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &PDInferenceServiceCustomDefaulter{}

// Default injects defaults:
//   - engine = sglang
//   - router.strategy = round-robin
//   - modelStorage.mountPath = /models
func (d *PDInferenceServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	pdis, ok := obj.(*pdaiv1alpha1.PDInferenceService)
	if !ok {
		return fmt.Errorf("expected a PDInferenceService but got %T", obj)
	}

	if pdis.Spec.Engine == "" {
		pdis.Spec.Engine = pdaiv1alpha1.EngineTypeSGLang
	}
	if pdis.Spec.Router == nil {
		pdis.Spec.Router = &pdaiv1alpha1.RouterSpec{Strategy: pdaiv1alpha1.RouterStrategyRoundRobin}
	}
	if pdis.Spec.ModelStorage.MountPath == "" {
		pdis.Spec.ModelStorage.MountPath = "/models"
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-pdai-pdai-io-v1alpha1-pdinferenceservice,mutating=false,failurePolicy=fail,sideEffects=None,groups=pdai.pdai.io,resources=pdinferenceservices,verbs=create;update,versions=v1alpha1,name=vpdinferenceservice-v1alpha1.kb.io,admissionReviewVersions=v1

// PDInferenceServiceCustomValidator validates PDInferenceService objects.
type PDInferenceServiceCustomValidator struct {
	Client client.Client
}

var _ webhook.CustomValidator = &PDInferenceServiceCustomValidator{}

// ValidateCreate enforces creation rules:
//   - Required fields present (model, modelStorage)
//   - images required when no engineProfileRef
//   - Profile must exist when engineProfileRef is set
//   - replicas >= 1
//   - kvTransfer.backend must be a valid enum value
//   - pdRatio and scaling.prefill are mutually exclusive
func (v *PDInferenceServiceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	pdis, ok := obj.(*pdaiv1alpha1.PDInferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a PDInferenceService but got %T", obj)
	}

	var errs field.ErrorList

	// model is required
	if pdis.Spec.Model == "" {
		errs = append(errs, field.Required(field.NewPath("spec", "model"), "model name is required"))
	}

	// modelStorage.type is required
	if pdis.Spec.ModelStorage.Type == "" {
		errs = append(errs, field.Required(field.NewPath("spec", "modelStorage", "type"), "storage type is required"))
	}

	// replicas >= 1
	if pdis.Spec.Prefill.Replicas < 1 {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "prefill", "replicas"),
			pdis.Spec.Prefill.Replicas,
			"prefill replicas must be >= 1",
		))
	}
	if pdis.Spec.Decode.Replicas < 1 {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "decode", "replicas"),
			pdis.Spec.Decode.Replicas,
			"decode replicas must be >= 1",
		))
	}

	// images required when no profile ref
	if pdis.Spec.EngineProfileRef == "" {
		if pdis.Spec.Images == nil ||
			pdis.Spec.Images.Scheduler == "" ||
			pdis.Spec.Images.Prefill == "" ||
			pdis.Spec.Images.Decode == "" {
			errs = append(errs, field.Required(
				field.NewPath("spec", "images"),
				"images (scheduler, prefill, decode) are required when engineProfileRef is not set",
			))
		}
	} else {
		// Profile must exist
		if err := v.validateProfileExists(ctx, pdis.Spec.EngineProfileRef); err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "engineProfileRef"),
				pdis.Spec.EngineProfileRef,
				err.Error(),
			))
		}
	}

	// kvTransfer.backend enum validation
	if pdis.Spec.EngineConfig != nil && pdis.Spec.EngineConfig.KVTransfer != nil {
		backend := pdis.Spec.EngineConfig.KVTransfer.Backend
		switch backend {
		case pdaiv1alpha1.KVBackendMooncake, pdaiv1alpha1.KVBackendNixl, pdaiv1alpha1.KVBackendNccl:
			// valid
		default:
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "engineConfig", "kvTransfer", "backend"),
				backend,
				"invalid backend: must be one of mooncake, nixl, nccl",
			))
		}
	}

	// pdRatio and scaling.prefill are mutually exclusive
	if pdis.Spec.PDRatio != "" && pdis.Spec.Scaling != nil && pdis.Spec.Scaling.Prefill != nil {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "pdRatio"),
			pdis.Spec.PDRatio,
			"pdRatio and scaling.prefill are mutually exclusive",
		))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}

// ValidateUpdate enforces immutability rules. Only prefill.replicas and decode.replicas may change.
func (v *PDInferenceServiceCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldPDIS, ok := oldObj.(*pdaiv1alpha1.PDInferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a PDInferenceService for oldObj but got %T", oldObj)
	}
	newPDIS, ok := newObj.(*pdaiv1alpha1.PDInferenceService)
	if !ok {
		return nil, fmt.Errorf("expected a PDInferenceService for newObj but got %T", newObj)
	}

	var errs field.ErrorList

	if oldPDIS.Spec.Model != newPDIS.Spec.Model {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "model"),
			"field is immutable after creation",
		))
	}
	if oldPDIS.Spec.ModelStorage != newPDIS.Spec.ModelStorage {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "modelStorage"),
			"field is immutable after creation",
		))
	}
	if oldPDIS.Spec.Engine != newPDIS.Spec.Engine {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "engine"),
			"field is immutable after creation",
		))
	}
	if oldPDIS.Spec.EngineProfileRef != newPDIS.Spec.EngineProfileRef {
		errs = append(errs, field.Forbidden(
			field.NewPath("spec", "engineProfileRef"),
			"field is immutable after creation",
		))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}

// ValidateDelete allows all deletions.
func (v *PDInferenceServiceCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateProfileExists checks that the named PDEngineProfile exists in pd-system namespace.
func (v *PDInferenceServiceCustomValidator) validateProfileExists(ctx context.Context, name string) error {
	profile := &pdaiv1alpha1.PDEngineProfile{}
	if err := v.Client.Get(ctx, types.NamespacedName{
		Namespace: "pd-system",
		Name:      name,
	}, profile); err != nil {
		return fmt.Errorf("PDEngineProfile %q not found in pd-system: %w", name, err)
	}
	return nil
}
