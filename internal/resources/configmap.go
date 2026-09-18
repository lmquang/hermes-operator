package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	hermesv1 "github.com/paperclipinc/hermes-operator/api/v1"
)

// ConfigMapName returns the deterministic ConfigMap name for the rendered config.
func ConfigMapName(inst *hermesv1.HermesInstance) string {
	return inst.Name + "-config"
}

// BuildConfigMap returns the desired ConfigMap holding ~/.hermes/config.yaml.
//
// `resolvedBody` is the body the reconciler has already resolved for the case
// where spec.config.configMapRef is set. The builder is pure: it does not
// reach out to the apiserver.
//
//   - Empty resolvedBody + Raw set         → use Raw verbatim (YAML-serialised).
//   - Empty resolvedBody + Raw unset       → emit "{}\n".
//   - resolvedBody non-empty + Raw unset   → use resolvedBody verbatim.
//   - resolvedBody non-empty + Raw set     → caller is responsible for merging
//     (use MergeYAMLBodies) and passing the merged result as resolvedBody.
func BuildConfigMap(inst *hermesv1.HermesInstance, resolvedBody string) *corev1.ConfigMap {
	body := "{}\n"
	switch {
	case resolvedBody != "":
		body = resolvedBody
	case inst.Spec.Config.Raw != nil && len(inst.Spec.Config.Raw.Raw) > 0:
		y, err := yaml.JSONToYAML(inst.Spec.Config.Raw.Raw)
		if err == nil {
			body = string(y)
		}
	}
	if merged, err := mergeGatewayFragments(body, BuildGatewayConfigFragments(inst)); err == nil {
		body = merged
	}
	// The upstream `gateway run` refuses to start without an LLM provider
	// configured. Ensure one is present so the instance can reach Ready: a real
	// deployment sets spec.config.raw with a `model:`/provider (and credentials
	// via env-from-secret); when none is given we inject a non-routable placeholder
	// so the gateway + API server come up (and serve /health) without making any
	// live LLM calls. Inference then fails clearly until a real provider is set.
	if withModel, err := ensureModelDefault(body); err == nil {
		body = withModel
	}
	// On parse error we keep the original body: the validating webhook is
	// responsible for rejecting malformed config; we don't want a pure builder
	// to panic.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(inst),
			Namespace: inst.Namespace,
			Labels:    LabelsForInstance(inst),
		},
		Data: map[string]string{
			"config.yaml": body,
		},
	}
	if tailscaleEnabled(inst) {
		cm.Data[tailscaleServeKey] = BuildTailscaleServeConfig(inst)
	}
	return cm
}

// mergeGatewayFragments deep-merges builder-derived gateway config fragments
// under the top-level `platforms` key of the rendered config.yaml. Gateway
// fragments win over any user-provided `platforms:` entries for the keys they
// set because the operator owns this sub-tree (users should disable the
// gateway and write their own if they need full control) -- a per-platform
// block is replaced wholesale, not deep-merged, so a user-authored
// `platforms.<name>.extra.*` alongside an *enabled* typed gateway is dropped;
// leave that gateway disabled in spec.gateways and rely on spec.config.raw
// alone if extra.* fields (e.g. Discord's allow_from, which has no typed
// field or env var yet) are needed.
//
// `platforms` (flat, not nested under a `gateway:` wrapper) is what
// gateway/config_loader.py's merge_platform_layers() actually reads
// (confirmed against the hermes-agent source at v2026.9.14) -- the previous
// `gateways` (plural) key here was never a schema hermes-agent recognizes,
// so every typed spec.gateways.* field silently had no effect on the
// rendered config (the DISCORD_BOT_TOKEN-style env vars in
// BuildGatewayEnv were unaffected, since those are read directly by
// hermes-agent's env-override layer, not through config.yaml at all).
func mergeGatewayFragments(body string, frags map[string]any) (string, error) {
	if len(frags) == 0 {
		return body, nil
	}
	root := map[string]any{}
	if body != "" {
		if err := yaml.Unmarshal([]byte(body), &root); err != nil {
			return "", fmt.Errorf("parse rendered config: %w", err)
		}
	}
	existing, _ := root["platforms"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range frags {
		existing[k] = v
	}
	root["platforms"] = existing
	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal merged config: %w", err)
	}
	return string(out), nil
}

// ensureModelDefault guarantees the rendered config has a top-level `model` so
// `hermes gateway run` can initialise. If the user already set one (directly or
// via spec.config), it is left untouched. Otherwise a non-routable placeholder
// provider is injected — enough for the gateway + API server to start and serve
// /health, but with no reachable upstream, so it never makes a live LLM call.
func ensureModelDefault(body string) (string, error) {
	root := map[string]any{}
	if body != "" {
		if err := yaml.Unmarshal([]byte(body), &root); err != nil {
			return "", fmt.Errorf("parse rendered config: %w", err)
		}
	}
	if _, ok := root["model"]; ok {
		return body, nil
	}
	root["model"] = "gpt-4o-mini"
	root["base_url"] = "http://127.0.0.1:9/v1"
	root["api_key"] = "placeholder-no-live-calls"
	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal config with model default: %w", err)
	}
	return string(out), nil
}

// MergeYAMLBodies performs a YAML deep-merge of `overlay` (JSON or YAML) onto
// `base` (YAML). Overlay wins on conflict. Used when spec.config.mergeMode=merge.
func MergeYAMLBodies(base, overlay string) (string, error) {
	baseMap := map[string]any{}
	if base != "" {
		if err := yaml.Unmarshal([]byte(base), &baseMap); err != nil {
			return "", fmt.Errorf("parse base YAML: %w", err)
		}
	}
	overlayMap := map[string]any{}
	if overlay != "" {
		if err := yaml.Unmarshal([]byte(overlay), &overlayMap); err != nil {
			return "", fmt.Errorf("parse overlay: %w", err)
		}
	}
	merged := deepMergeMaps(baseMap, overlayMap)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("marshal merged: %w", err)
	}
	return string(out), nil
}

func deepMergeMaps(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if bv, ok := out[k]; ok {
			bm, bok := bv.(map[string]any)
			vm, vok := v.(map[string]any)
			if bok && vok {
				out[k] = deepMergeMaps(bm, vm)
				continue
			}
		}
		out[k] = v
	}
	return out
}
