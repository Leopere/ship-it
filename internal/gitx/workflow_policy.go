package gitx

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type hostedRunnerFinding struct {
	Job       string
	Selection string
	Line      int
}

func hostedRunnerFindings(data []byte) ([]hostedRunnerFinding, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, nil
	}
	jobs, _ := mappingEntry(document.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, nil
	}

	var findings []hostedRunnerFinding
	for index := 0; index+1 < len(jobs.Content); index += 2 {
		jobName := jobs.Content[index]
		job := jobs.Content[index+1]
		if job.Kind != yaml.MappingNode {
			continue
		}
		runsOn, runsOnKey := mappingEntry(job, "runs-on")
		if runsOn == nil {
			if uses, _ := mappingEntry(job, "uses"); uses != nil {
				continue
			}
			findings = append(findings, hostedRunnerFinding{
				Job: jobName.Value, Selection: "missing runs-on", Line: jobName.Line,
			})
			continue
		}
		if containsSelfHostedLabel(runsOn) {
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

func containsSelfHostedLabel(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode {
		return containsSelfHostedLabel(node.Alias)
	}
	if node.Kind == yaml.ScalarNode && strings.EqualFold(strings.TrimSpace(node.Value), "self-hosted") {
		return true
	}
	for _, child := range node.Content {
		if containsSelfHostedLabel(child) {
			return true
		}
	}
	return false
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
	if node.Kind == yaml.AliasNode {
		collectScalarValues(node.Alias, values)
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
