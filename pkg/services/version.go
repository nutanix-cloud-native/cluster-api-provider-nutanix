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
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const pcVersion202xPattern = `^202(\d(\.\d+)+)$`

const pcVersion7xPattern = `^7((\.\d+)+)$`

// is202XPcVersion checks if the version is of the format 202x.xx.xx..
func is202XPcVersion(version string) (bool, error) {
	re, err := regexp.Compile(pcVersion202xPattern)
	if err != nil {
		return false, fmt.Errorf("failed to compile PC 202x version regex: %w", err)
	}
	return re.MatchString(version), nil
}

// is7XPcVersion checks if the version is of the format 7.xx.xx..
func is7XPcVersion(version string) (bool, error) {
	re, err := regexp.Compile(pcVersion7xPattern)
	if err != nil {
		return false, fmt.Errorf("failed to compile PC 7.x version regex: %w", err)
	}
	return re.MatchString(version), nil
}

// CleanPCVersion normalizes a Prism Central version string by trimming whitespace,
// lower-casing it, and removing the optional "pc." prefix.
func CleanPCVersion(version string) string {
	lowerVersion := strings.ToLower(strings.TrimSpace(version))
	return strings.TrimPrefix(lowerVersion, "pc.")
}

func convertStringToIntList(str string) []int {
	strList := strings.Split(str, ".")
	var intList []int
	for _, x := range strList {
		if val, err := strconv.Atoi(x); err != nil {
			return []int{9999}
		} else {
			intList = append(intList, val)
		}
	}
	return intList
}

// CompareVersions compares version numbers of the format '3.5.2.1'.
// Returns 0 : if v1 == v2
// Returns 1 : if v1 > v2
// Returns -1: if v1 < v2
//
// If either version is not in the correct format, they will be the greater,
// unless neither can be parsed in which case they are equal. The case where a
// branch can't be parsed is if the cluster is running master, or some other
// non-release branch, or empty string. This is only expected in a test/debug
// situation and is the motivation for making an unparseable format greater.
func CompareVersions(v1, v2 string) int {
	if strings.EqualFold(v1, "master") {
		v1 = "9999"
	}
	if strings.EqualFold(v2, "master") {
		v2 = "9999"
	}

	v1IntList := convertStringToIntList(v1)
	v2IntList := convertStringToIntList(v2)

	maxLen := max(len(v1IntList), len(v2IntList))

	v1NormIntList := make([]int, maxLen)
	v2NormIntList := make([]int, maxLen)
	copy(v1NormIntList, v1IntList)
	copy(v2NormIntList, v2IntList)

	for i, e := range v1NormIntList {
		if e > v2NormIntList[i] {
			return 1
		} else if e < v2NormIntList[i] {
			return -1
		}
	}
	return 0
}

// ComparePCVersions compares PC version numbers of the format '2024.2.0.1', '7.3', etc.
// Returns 0 : if ver1 == ver2
// Returns 1 : if ver1 > ver2
// Returns -1: if ver1 < ver2
//
// If either version is not in the correct format, they will be the greater,
// unless neither can be parsed in which case they are equal. The case where a
// branch can't be parsed is if the cluster is running master, or some other
// non-release branch. This is only expected in a test/debug situation and is
// the motivation for making an unparseable format greater.
// A non-nil error means a version-format regex failed to compile.
func ComparePCVersions(v1, v2 string) (int, error) {
	cleanV1 := CleanPCVersion(v1)
	cleanV2 := CleanPCVersion(v2)

	v1Is7, err := is7XPcVersion(cleanV1)
	if err != nil {
		return 0, err
	}
	v2Is202, err := is202XPcVersion(cleanV2)
	if err != nil {
		return 0, err
	}
	// Special case for comparing PC versions of format 7.xx and 202x.xx.xx
	if v1Is7 && v2Is202 {
		return 1, nil
	}

	v2Is7, err := is7XPcVersion(cleanV2)
	if err != nil {
		return 0, err
	}
	v1Is202, err := is202XPcVersion(cleanV1)
	if err != nil {
		return 0, err
	}
	if v2Is7 && v1Is202 {
		return -1, nil
	}

	return CompareVersions(cleanV1, cleanV2), nil
}

// IsPCVersionHigherThan75 returns true if the PC version is >= 7.6. Version may include a "pc." prefix (e.g. "pc.7.6.0.5").
func IsPCVersionHigherThan75(version string) bool {
	v := CleanPCVersion(version)
	if v == "" {
		return false
	}
	return CompareVersions(v, "7.6") >= 0
}
