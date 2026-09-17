package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hermesv1 "github.com/paperclipinc/hermes-operator/api/v1"
)

func TestBuildSkillsInitContainer_NoSkillsMeansNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, BuildSkillsInitContainer(minimalInstance()))
}

func TestBuildSkillsInitContainer_ClonesEachSource(t *testing.T) {
	t.Parallel()
	inst := minimalInstance()
	inst.Spec.Skills = []hermesv1.InstanceSkill{
		{Source: "git+https://github.com/foo/finance-skill@v1.2.0"},
		{Source: "https://github.com/foo/no-ref-skill"},
	}

	c := BuildSkillsInitContainer(inst)
	require.NotNil(t, c)
	assert.Equal(t, "init-skills", c.Name)
	assert.Equal(t, GitInitImage, c.Image)
	require.Len(t, c.Args, 2)
	script := c.Args[1]

	assert.Contains(t, script, `git clone --depth 1 --branch "v1.2.0" "https://github.com/foo/finance-skill" "/home/hermes/.hermes/skills/finance-skill"`)
	assert.Contains(t, script, `git clone --depth 1 "https://github.com/foo/no-ref-skill" "/home/hermes/.hermes/skills/no-ref-skill"`)

	var sawDataMount bool
	for _, m := range c.VolumeMounts {
		if m.Name == "data" && m.MountPath == "/home/hermes/.hermes" {
			sawDataMount = true
		}
	}
	assert.True(t, sawDataMount, "mounts the data PVC at ~/.hermes so cloned skills persist")
}

func TestParseSkillSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		source        string
		wantRemote    string
		wantRef       string
		wantSkillName string
	}{
		{
			name:          "git+ prefix with ref",
			source:        "git+https://github.com/foo/finance-skill@v1.2.0",
			wantRemote:    "https://github.com/foo/finance-skill",
			wantRef:       "v1.2.0",
			wantSkillName: "finance-skill",
		},
		{
			name:          "plain url, no ref",
			source:        "https://github.com/foo/bar",
			wantRemote:    "https://github.com/foo/bar",
			wantRef:       "",
			wantSkillName: "bar",
		},
		{
			name:          ".git suffix stripped from derived name",
			source:        "https://github.com/foo/bar.git",
			wantRemote:    "https://github.com/foo/bar.git",
			wantRef:       "",
			wantSkillName: "bar",
		},
		{
			name:          "basic-auth credentials not mistaken for a ref",
			source:        "https://user:pass@github.com/foo/bar",
			wantRemote:    "https://user:pass@github.com/foo/bar",
			wantRef:       "",
			wantSkillName: "bar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			remote, ref, name := parseSkillSource(tc.source)
			assert.Equal(t, tc.wantRemote, remote)
			assert.Equal(t, tc.wantRef, ref)
			assert.Equal(t, tc.wantSkillName, name)
		})
	}
}
