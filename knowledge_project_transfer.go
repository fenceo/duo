package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const workspaceMaxKnowledgeProjects = 500

// The optional archive field carries project labels and an opaque identity,
// never environment configuration, task paths or execution credentials.
func archiveKnowledgeProject(project KnowledgeProject) (KnowledgeProject, error) {
	if !archiveIDPattern.MatchString(project.ID) || !curatedSecretFree(project.ID) {
		return KnowledgeProject{}, errors.New("导入知识项目标识无效")
	}
	prepared, err := prepareKnowledgeProject(project.Name, project.Aliases)
	if err != nil {
		return KnowledgeProject{}, errors.New("知识项目名称或别名无效")
	}
	for _, label := range append([]string{prepared.Name}, prepared.Aliases...) {
		if redactWorkspaceText(label) != label {
			return KnowledgeProject{}, errors.New("知识项目名称和别名不可包含本机路径或凭据")
		}
	}
	prepared.ID = project.ID
	return prepared, nil
}

func archiveKnowledgeProjectSignature(project KnowledgeProject) string {
	labels := []string{knowledgeScopeAlias(project.Name)}
	for _, alias := range project.Aliases {
		labels = append(labels, knowledgeScopeAlias(alias))
	}
	sort.Strings(labels)
	return strings.Join(labels, "\n")
}

func validateWorkspaceKnowledgeProjects(tasks []workspaceArchiveTask) error {
	if len(tasks) > workspaceMaxTasks {
		return errors.New("工作区任务超过导入导出上限")
	}
	identities := map[string]string{}
	for _, task := range tasks {
		if task.KnowledgeProject == nil {
			continue // Archives from before project assignments remain unchanged.
		}
		project, err := archiveKnowledgeProject(*task.KnowledgeProject)
		if err != nil {
			return err
		}
		signature := archiveKnowledgeProjectSignature(project)
		if previous, ok := identities[project.ID]; ok && previous != signature {
			return errors.New("同一知识项目标识包含冲突的名称或别名")
		}
		identities[project.ID] = signature
		if len(identities) > workspaceMaxKnowledgeProjects {
			return errors.New("工作区知识项目超过 500 个")
		}
	}
	return nil
}

type workspaceKnowledgeProjectReport struct {
	Projects int
	Tasks    int
	Warnings []string
}

// Imports are additive: conflicting identities never overwrite a local
// project's labels or an existing task assignment. Labels may reconcile an
// identity across installations; multiple possible targets require a choice.
func importWorkspaceKnowledgeProjects(tx *sql.Tx, tasks []workspaceArchiveTask, taskIDs map[string]string) (workspaceKnowledgeProjectReport, error) {
	report := workspaceKnowledgeProjectReport{Warnings: []string{}}
	if err := validateWorkspaceKnowledgeProjects(tasks); err != nil {
		return report, err
	}
	hasProjects := false
	for _, task := range tasks {
		if task.KnowledgeProject != nil {
			hasProjects = true
			break
		}
	}
	if !hasProjects {
		return report, nil
	}
	rows, err := tx.Query("SELECT id,name,aliases FROM knowledge_projects ORDER BY id LIMIT 501")
	if err != nil {
		return report, err
	}
	projects := []KnowledgeProject{}
	for rows.Next() {
		var project KnowledgeProject
		var raw string
		if err = rows.Scan(&project.ID, &project.Name, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &project.Aliases); err != nil {
			break
		}
		projects = append(projects, project)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return report, err
	}
	if len(projects) > workspaceMaxKnowledgeProjects {
		return report, errors.New("当前知识项目超过 500 个，请先整理项目配置")
	}
	mapping := map[string]string{}
	bound := map[string]bool{}
	for _, task := range tasks {
		if task.KnowledgeProject == nil {
			continue
		}
		incoming, err := archiveKnowledgeProject(*task.KnowledgeProject)
		if err != nil {
			return report, err
		}
		projectID, processed := mapping[incoming.ID]
		if !processed {
			matches, idExists := []int{}, false
			for i, project := range projects {
				if project.ID == incoming.ID {
					idExists = true
				}
				for _, label := range append([]string{incoming.Name}, incoming.Aliases...) {
					if knowledgeProjectMatchesScope(&project, label) {
						matches = append(matches, i)
						break
					}
				}
			}
			switch {
			case len(matches) > 1:
				report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”的名称或别名对应多个已有项目，未绑定导入任务；请在任务记录中手动选择。", incoming.Name))
			case len(matches) == 1:
				existing := projects[matches[0]]
				projectID = existing.ID
				if idExists && existing.ID != incoming.ID {
					report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”的导入标识与其他项目冲突，已按名称或别名复用已有项目；原项目与任务绑定保持不变。", incoming.Name))
				}
				for _, label := range append([]string{incoming.Name}, incoming.Aliases...) {
					if !knowledgeProjectMatchesScope(&existing, label) {
						report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”复用已有项目“%s”；新增名称或别名未自动合入，沿用本机项目设置。", incoming.Name, existing.Name))
						break
					}
				}
			case len(projects) >= workspaceMaxKnowledgeProjects:
				report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”未绑定：本机项目已达 500 个上限，请整理后手动选择。", incoming.Name))
			default:
				projectID = incoming.ID
				if idExists {
					projectID = uid()
					report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”的导入标识已被其他项目使用，已分配新标识；原项目与任务绑定保持不变。", incoming.Name))
				}
				incoming.ID = projectID
				raw, _ := json.Marshal(incoming.Aliases)
				if _, err = tx.Exec("INSERT INTO knowledge_projects(id,name,name_key,aliases,created) VALUES(?,?,?,?,?)", incoming.ID, incoming.Name, strings.ToLower(incoming.Name), string(raw), now()); err != nil {
					return report, err
				}
				projects = append(projects, incoming)
			}
			mapping[task.KnowledgeProject.ID] = projectID
		}
		if projectID == "" {
			continue
		}
		mappedTask := taskIDs[task.ID]
		if mappedTask == "" {
			return report, errors.New("导入知识项目缺少任务映射")
		}
		var previous string
		err = tx.QueryRow("SELECT project_id FROM task_knowledge_projects WHERE task_id=?", mappedTask).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		if previous != "" && previous != projectID {
			report.Warnings = append(report.Warnings, fmt.Sprintf("项目“%s”未改写任务已有的知识项目绑定，请在任务记录中核对。", incoming.Name))
			continue
		}
		if _, err = tx.Exec("INSERT INTO task_knowledge_projects(task_id,project_id) VALUES(?,?) ON CONFLICT(task_id) DO NOTHING", mappedTask, projectID); err != nil {
			return report, err
		}
		report.Tasks++
		bound[projectID] = true
	}
	report.Projects = len(bound)
	return report, nil
}
