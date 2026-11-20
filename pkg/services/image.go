/*
Copyright 2022 Nutanix

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

package services

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"

	vmmconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
	"k8s.io/utils/ptr"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
	"github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
)

// isImageUsableForProject returns true if the image is accessible from the
// given project, i.e. it is either owned by the project or shared with all
// projects. PC < 7.6 has no concept of project ownership / sharing for images,
// so callers must gate this check on the PC version before invoking it.
func isImageUsableForProject(image *imageModels.Image, projectExtID string) bool {
	if image == nil {
		return false
	}
	// Directly owned by the project.
	if image.ProjectExtId != nil && *image.ProjectExtId == projectExtID {
		return true
	}
	// Shared with all projects.
	if image.IsSharedWithAllProjects != nil && *image.IsSharedWithAllProjects {
		return true
	}
	return false
}

// GetImage returns an image. If no UUID is provided, returns the unique image with the name.
// Returns an error if no image has the UUID, if no image has the name, or more than one image has the name.
//
// When project is non-nil and the PC version supports projects (>= 7.6),
// the result is scoped to the given project: a UUID lookup must resolve to an
// image that is owned by or shared with the project, and a name lookup retains
// only images accessible from the project. During name-based lookups a
// project-owned image is preferred over a shared image of the same name, so the
// presence of both is not treated as ambiguous. On PC < 7.6 (or when
// project is nil) the legacy, unscoped behavior is preserved.
//
//nolint:gocognit // project-aware lookup requires branching on UUID/name and ownership/shared state
func GetImage(ctx context.Context, client *v4Converged.Client, id infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo, pcVersion string) (*imageModels.Image, error) {
	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}

	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-all-projects does not exist, so
	// we fall back to the legacy (unscoped) lookup behavior.
	projectAware := IsPCVersionHigherThan75(pcVersion) && projectExtID != nil

	switch {
	case id.IsUUID():
		resp, err := client.Images.Get(ctx, *id.UUID)
		if err != nil {
			if converged.IsNotFound(err) {
				return nil, fmt.Errorf("failed to find image with UUID %s: %w", *id.UUID, err)
			}
			return nil, fmt.Errorf("failed to get image with UUID %s: %w", *id.UUID, err)
		}
		if projectAware && !isImageUsableForProject(resp, *projectExtID) {
			return nil, &terminalError{message: fmt.Sprintf(
				"image with UUID %s is not accessible in project %s", *id.UUID, *project.Name)}
		}
		return resp, nil
	case id.IsName():
		responseImages, err := client.Images.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *id.Name)))
		if err != nil {
			return nil, err
		}

		if !projectAware {
			// Legacy behavior: exact name match, must resolve to a single image.
			foundImages := make([]*imageModels.Image, 0)
			for i := range responseImages {
				if responseImages[i].Name != nil && strings.EqualFold(*responseImages[i].Name, *id.Name) {
					foundImages = append(foundImages, &responseImages[i])
				}
			}
			switch len(foundImages) {
			case 0:
				return nil, &terminalError{message: fmt.Sprintf("found no image with name %s", *id.Name)}
			case 1:
				return foundImages[0], nil
			default:
				return nil, fmt.Errorf("more than one image found with name %s", *id.Name)
			}
		}

		// Project-aware: partition the usable name matches into images owned by
		// this project versus images merely shared with it.
		var owned, shared []*imageModels.Image
		for i := range responseImages {
			img := &responseImages[i]
			if img.Name == nil || !strings.EqualFold(*img.Name, *id.Name) {
				continue
			}
			if !isImageUsableForProject(img, *projectExtID) {
				continue
			}
			if img.ProjectExtId != nil && *img.ProjectExtId == *projectExtID {
				owned = append(owned, img)
			} else {
				shared = append(shared, img)
			}
		}

		// Prefer a project-owned image over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return owned[0], nil
		case len(owned) > 1:
			return nil, fmt.Errorf(
				"more than one image found with name %s owned by project %s", *id.Name, *project.Name)
		case len(shared) == 1:
			return shared[0], nil
		case len(shared) > 1:
			return nil, fmt.Errorf(
				"more than one image found with name %s shared with project %s and none owned by it",
				*id.Name, *project.Name)
		default:
			return nil, &terminalError{message: fmt.Sprintf(
				"found no image with name %s accessible in project %s", *id.Name, *project.Name)}
		}
	default:
		return nil, fmt.Errorf("image identifier is missing both name and uuid")
	}
}

// isVMProfileUsableForProject returns true if the VM profile is accessible from
// the given project, i.e. it is either owned by the project or explicitly shared
// with it. PC < 7.6 has no concept of project ownership / sharing for VM
// profiles, so callers must gate this check on the PC version before invoking it.
func isVMProfileUsableForProject(profile *vmmconfig.VmProfile, projectExtID string) bool {
	if profile == nil {
		return false
	}
	// Directly owned by the project.
	if profile.ProjectExtId != nil && *profile.ProjectExtId == projectExtID {
		return true
	}
	// Explicitly shared with the project.
	return slices.Contains(profile.SharedWithProjects, projectExtID)
}

// GetVMProfile returns a VM profile scoped to the given project. VM profiles are only
// supported on PC 7.6 or later, so the lookup is always project-aware: a UUID lookup must
// resolve to a profile owned by or shared with the project, and a name lookup retains only
// profiles accessible from the project, preferring a project-owned profile over a shared
// one of the same name (so the presence of both is not treated as ambiguous).
func GetVMProfile(ctx context.Context, client *v4Converged.Client, id infrav1.NutanixResourceIdentifier, project *nctx.ProjectInfo) (*vmmconfig.VmProfile, error) {
	if project == nil || project.ExtID == nil {
		return nil, fmt.Errorf("project must be set to look up VM profile %s", id.String())
	}
	projectExtID := *project.ExtID

	switch {
	case id.IsUUID():
		profile, err := client.VMProfiles.Get(ctx, *id.UUID)
		if err != nil {
			return nil, fmt.Errorf("failed to find VM profile with UUID %s: %w", *id.UUID, err)
		}
		if !isVMProfileUsableForProject(profile, projectExtID) {
			return nil, &terminalError{message: fmt.Sprintf(
				"VM profile with UUID %s is not accessible in project %s", *id.UUID, *project.Name)}
		}
		return profile, nil
	case id.IsName():
		responseProfiles, err := client.VMProfiles.List(ctx, converged.WithFilter(fmt.Sprintf("name eq '%s'", *id.Name)))
		if err != nil {
			return nil, err
		}

		// Partition the usable name matches into profiles owned by this project
		// versus profiles merely shared with it.
		var owned, shared []*vmmconfig.VmProfile
		for i := range responseProfiles {
			p := &responseProfiles[i]
			if p.Name == nil || !strings.EqualFold(*p.Name, *id.Name) {
				continue
			}
			if !isVMProfileUsableForProject(p, projectExtID) {
				continue
			}
			if p.ProjectExtId != nil && *p.ProjectExtId == projectExtID {
				owned = append(owned, p)
			} else {
				shared = append(shared, p)
			}
		}

		// Prefer a project-owned profile over a shared one of the same name.
		switch {
		case len(owned) == 1:
			return owned[0], nil
		case len(owned) > 1:
			return nil, fmt.Errorf(
				"more than one VM profile found with name %s owned by project %s", *id.Name, *project.Name)
		case len(shared) == 1:
			return shared[0], nil
		case len(shared) > 1:
			return nil, fmt.Errorf(
				"more than one VM profile found with name %s shared with project %s and none owned by it",
				*id.Name, *project.Name)
		default:
			return nil, &terminalError{message: fmt.Sprintf(
				"found no VM profile with name %s accessible in project %s", *id.Name, *project.Name)}
		}
	default:
		return nil, fmt.Errorf("VM profile identifier is missing both name and uuid")
	}
}

type ImageLookup struct {
	BaseOS     string
	K8sVersion string
}

func GetImageByLookup(
	ctx context.Context,
	client *v4Converged.Client,
	imageTemplate,
	imageLookupBaseOS,
	k8sVersion *string,
	project *nctx.ProjectInfo,
	pcVersion string,
) (*imageModels.Image, error) {
	if imageTemplate == nil || imageLookupBaseOS == nil || k8sVersion == nil {
		return nil, fmt.Errorf(
			"image lookup requires non-nil imageTemplate, imageLookupBaseOS, and k8sVersion",
		)
	}

	var projectExtID *string
	if project != nil {
		projectExtID = project.ExtID
	}

	if strings.Contains(*k8sVersion, "v") {
		k8sVersion = ptr.To(strings.Replace(*k8sVersion, "v", "", 1))
	}
	params := ImageLookup{*imageLookupBaseOS, *k8sVersion}
	t, err := template.New("k8sTemplate").Parse(*imageTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template given %s: %w", *imageTemplate, err)
	}
	var templateBytes bytes.Buffer
	err = t.Execute(&templateBytes, params)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to substitute string %s with params %v error: %w",
			*imageTemplate,
			params,
			err,
		)
	}
	responseImages, err := client.Images.List(ctx)
	if err != nil {
		return nil, err
	}
	// projectAware indicates project scoping should be enforced. On PC < 7.6 the
	// concept of project ownership / shared-with-all-projects does not exist, so
	// we fall back to the legacy (unscoped) lookup behavior.
	projectAware := IsPCVersionHigherThan75(pcVersion) && projectExtID != nil

	re, err := regexp.Compile(templateBytes.String())
	if err != nil {
		return nil, fmt.Errorf("failed to compile image lookup regex %q: %w", templateBytes.String(), err)
	}
	// Partition the regex survivors into images owned by this project versus
	// images merely shared with it. When not project-aware everything lands in
	// owned, preserving the legacy behavior.
	owned := make([]*imageModels.Image, 0)
	shared := make([]*imageModels.Image, 0)
	for i := range responseImages {
		img := &responseImages[i]
		if img.Name == nil || !re.Match([]byte(*img.Name)) {
			continue
		}
		if !projectAware {
			owned = append(owned, img)
			continue
		}
		if !isImageUsableForProject(img, *projectExtID) {
			continue
		}
		if img.ProjectExtId != nil && *img.ProjectExtId == *projectExtID {
			owned = append(owned, img)
		} else {
			shared = append(shared, img)
		}
	}

	// Prefer project-owned images over shared ones; only fall back to shared
	// images when the project owns none matching the lookup.
	candidates := owned
	if len(candidates) == 0 {
		candidates = shared
	}
	sorted := sortImagesByLatestCreationTime(candidates)
	if len(sorted) == 0 {
		if projectAware {
			return nil, &terminalError{message: fmt.Sprintf(
				"failed to find image with filter %s accessible in project %s",
				templateBytes.String(), *project.Name)}
		}
		return nil, &terminalError{message: fmt.Sprintf("failed to find image with filter %s", templateBytes.String())}
	}
	return sorted[0], nil
}

// returns the images with the latest creation time first.
func sortImagesByLatestCreationTime(
	images []*imageModels.Image,
) []*imageModels.Image {
	sort.Slice(images, func(i, j int) bool {
		if images[i].CreateTime == nil || images[j].CreateTime == nil {
			return images[i].CreateTime != nil
		}
		timeI := *images[i].CreateTime
		timeJ := *images[j].CreateTime
		return timeI.After(timeJ)
	})
	return images
}

func ImageMarkedForDeletion(ctx context.Context, client *v4Converged.Client, image *imageModels.Image) (bool, error) {
	filterString := fmt.Sprintf(
		"entitiesAffected/any(a:a/extId eq '%s') "+
			"and (status eq Prism.Config.TaskStatus'RUNNING' or status eq Prism.Config.TaskStatus'QUEUED') "+
			"and (operation eq 'kImageDelete')",
		*image.ExtId)
	tasks, err := client.Tasks.List(ctx, converged.WithFilter(filterString))
	if err != nil {
		return false, err
	}
	return len(tasks) > 0, nil
}
