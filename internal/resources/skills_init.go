package resources

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	hermesv1 "github.com/paperclipinc/hermes-operator/api/v1"
)

// GitInitImage is the init container image used to clone spec.skills sources.
const GitInitImage = "alpine/git:2.47.1"

// BuildSkillsInitContainer returns the init container that clones each
// spec.skills entry into /home/hermes/.hermes/skills/<name>. Returns nil when
// no skills are declared.
//
// hermes-agent discovers skills as directories containing a SKILL.md under
// ~/.hermes/skills/ -- not installable Python packages -- so despite
// spec.skills.source being documented as a "uv/pip-compatible install
// source", a git URL is the shape that actually produces a working skill
// here. Source is treated as a git remote, optionally suffixed with "@<ref>"
// (branch/tag/sha); a leading "git+" is stripped if present since that's the
// pip-style prefix the existing docs/examples already use for this field.
func BuildSkillsInitContainer(inst *hermesv1.HermesInstance) *corev1.Container {
	if len(inst.Spec.Skills) == 0 {
		return nil
	}

	var script strings.Builder
	script.WriteString("set -euo pipefail\n")
	for _, s := range inst.Spec.Skills {
		remote, ref, name := parseSkillSource(s.Source)
		dest := "/home/hermes/.hermes/skills/" + name
		script.WriteString(fmt.Sprintf("rm -rf %q\n", dest))
		if ref != "" {
			script.WriteString(fmt.Sprintf("git clone --depth 1 --branch %q %q %q\n", ref, remote, dest))
		} else {
			script.WriteString(fmt.Sprintf("git clone --depth 1 %q %q\n", remote, dest))
		}
		script.WriteString(fmt.Sprintf("echo %q >&2\n", "skill installed: "+s.Source+" -> "+dest))
	}

	return &corev1.Container{
		Name:                     "init-skills",
		Image:                    GitInitImage,
		ImagePullPolicy:          corev1.PullIfNotPresent,
		Command:                  []string{"/bin/sh"},
		Args:                     []string{"-c", script.String()},
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "data", MountPath: "/home/hermes/.hermes"},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: Ptr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// parseSkillSource splits a skill source into (remote, ref, name). Example:
// "git+https://github.com/foo/finance-skill@v1.2.0" ->
// remote="https://github.com/foo/finance-skill", ref="v1.2.0", name="finance-skill".
// The "@" split only fires when it falls after the last "/", so it does not
// mistake basic-auth "user:pass@host" credentials for a ref suffix.
func parseSkillSource(source string) (remote, ref, name string) {
	remote = strings.TrimPrefix(source, "git+")
	if i := strings.LastIndex(remote, "@"); i > strings.LastIndex(remote, "/") {
		ref = remote[i+1:]
		remote = remote[:i]
	}
	name = remote
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".git")
	return remote, ref, name
}
