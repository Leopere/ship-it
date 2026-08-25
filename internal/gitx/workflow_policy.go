package gitx

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var localRunnerLabelProfiles = [][]string{
	{"self-hosted", "Linux", "ARM64", "leopere", "local"},
	{"self-hosted", "Linux", "ARM64", "leopere", "local", "docker"},
	{"self-hosted", "Linux", "ARM64", "leopere", "local", "public-safe"},
	{"self-hosted", "macOS", "ARM64", "leopere", "local", "native"},
}

type hostedRunnerFinding struct {
	Job       string
	Selection string
	Line      int
}

func hostedRunnerFindings(data []byte, localWorkflows map[string]struct{}) ([]hostedRunnerFinding, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, nil
	}
	jobs, _ := mappingEntry(document.Content[0], "jobs")
	jobs = resolveAlias(jobs)
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, nil
	}

	var findings []hostedRunnerFinding
	for index := 0; index+1 < len(jobs.Content); index += 2 {
		jobName := jobs.Content[index]
		job := resolveAlias(jobs.Content[index+1])
		if job == nil || job.Kind != yaml.MappingNode {
			findings = append(findings, hostedRunnerFinding{
				Job: jobName.Value, Selection: "unverified job definition", Line: jobName.Line,
			})
			continue
		}
		runsOn, runsOnKey := mappingEntry(job, "runs-on")
		if runsOn == nil {
			if uses, usesKey := mappingEntry(job, "uses"); uses != nil {
				if workflowPath, local := localReusableWorkflowPath(uses); local {
					if _, present := localWorkflows[workflowPath]; present {
						continue
					}
					findings = append(findings, hostedRunnerFinding{
						Job: jobName.Value, Selection: "missing local reusable workflow " + summarizeRunnerSelection(uses), Line: usesKey.Line,
					})
					continue
				}
				findings = append(findings, hostedRunnerFinding{
					Job: jobName.Value, Selection: "unverified reusable workflow " + summarizeRunnerSelection(uses), Line: usesKey.Line,
				})
				continue
			}
			findings = append(findings, hostedRunnerFinding{
				Job: jobName.Value, Selection: "missing runs-on", Line: jobName.Line,
			})
			continue
		}
		if usesDocumentedLocalRunner(runsOn) {
			continue
		}
		findings = append(findings, hostedRunnerFinding{
			Job: jobName.Value, Selection: summarizeRunnerSelection(runsOn), Line: runsOnKey.Line,
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Line == findings[j].Line {
			return findings[i].Job < findings[j].Job
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func mappingEntry(mapping *yaml.Node, key string) (value, keyNode *yaml.Node) {
	mapping = resolveAlias(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], mapping.Content[index]
		}
	}
	return nil, nil
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	seen := make(map[*yaml.Node]struct{})
	for node != nil && node.Kind == yaml.AliasNode {
		if _, duplicate := seen[node]; duplicate {
			return nil
		}
		seen[node] = struct{}{}
		node = node.Alias
	}
	return node
}

func usesDocumentedLocalRunner(node *yaml.Node) bool {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.SequenceNode {
		return false
	}
	labels := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		child = resolveAlias(child)
		if child == nil || child.Kind != yaml.ScalarNode {
			return false
		}
		labels = append(labels, strings.TrimSpace(child.Value))
	}
	for _, profile := range localRunnerLabelProfiles {
		if sameLabels(labels, profile) {
			return true
		}
	}
	return false
}

func sameLabels(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	counts := make(map[string]int, len(actual))
	for _, label := range actual {
		counts[label]++
	}
	for _, label := range expected {
		if counts[label] != 1 {
			return false
		}
	}
	return true
}

func localReusableWorkflowPath(node *yaml.Node) (string, bool) {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	selection := strings.TrimSpace(node.Value)
	if strings.Contains(selection, "@") || strings.Contains(selection, "${{") {
		return "", false
	}
	var candidate string
	switch {
	case strings.HasPrefix(selection, "./"):
		candidate = strings.TrimPrefix(selection, "./")
	case strings.HasPrefix(selection, "$/"):
		candidate = strings.TrimPrefix(selection, "$/")
	default:
		return "", false
	}
	cleaned := path.Clean(candidate)
	if cleaned != candidate || path.Dir(cleaned) != ".github/workflows" {
		return "", false
	}
	extension := path.Ext(cleaned)
	return cleaned, extension == ".yml" || extension == ".yaml"
}

func summarizeRunnerSelection(node *yaml.Node) string {
	if node == nil {
		return "missing runs-on"
	}
	if node.Kind == yaml.AliasNode {
		return summarizeRunnerSelection(node.Alias)
	}
	var values []string
	collectScalarValues(node, &values)
	if len(values) == 0 {
		return "unverified runs-on value"
	}
	return strings.Join(values, ", ")
}

func collectScalarValues(node *yaml.Node, values *[]string) {
	node = resolveAlias(node)
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode {
		*values = append(*values, node.Value)
		return
	}
	for _, child := range node.Content {
		collectScalarValues(child, values)
	}
}

func formatHostedRunnerFindings(findings []hostedRunnerFinding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, fmt.Sprintf("%d:%s:%s", finding.Line, finding.Job, finding.Selection))
	}
	return strings.Join(parts, ",")
}
