/*
Copyright 2026 Nutanix

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
	"errors"
	"reflect"
	"regexp"
	"strings"

	"github.com/nutanix-cloud-native/prism-go-client/converged"
)

// ErrorClass is a charter-defined classification of an error returned by the
// Nutanix v4 API. It layers over the SDK's HTTP-status/errorGroup classification
// so that CAPX can make retry/terminal/accepted decisions by the specific
// VMM-xxxxx / CTGRS-xxxxx error code, not just the HTTP status.
//
// The taxonomy is sourced from the "CAPX Enterprise Readiness" charters:
//   - VMM (VM Management) error codes   -> vmmCodeClass
//   - CTGRS (Categories) error codes    -> ctgrsCodeClass
type ErrorClass int

const (
	// ClassUnknown means the error could not be classified from its code or
	// HTTP status. Callers should treat it as retriable (the safe default for
	// transient-looking failures).
	ClassUnknown ErrorClass = iota
	// ClassAccepted means the requested state is already satisfied (e.g. the
	// object already exists). Callers should treat the operation as a success
	// and proceed to GET/confirm-live.
	ClassAccepted
	// ClassRetriable means the same request can succeed on a later attempt.
	ClassRetriable
	// ClassTerminal means the same request will fail the same way again.
	// Callers should stamp a terminal failure and stop retrying.
	ClassTerminal
)

// FailedSubtasksSuffix separates a Prism task error from diagnostic text
// collected from its failed child tasks. ClassifyError ignores everything
// after this suffix: the suffix exists so status messages stay actionable,
// and it must not flip the parent error between retriable and terminal.
const FailedSubtasksSuffix = "; failed_subtasks: "

// codeRegexp matches charter error codes embedded anywhere in an error
// message or response body, e.g. "VMM-30101" or "CTGRS-50023".
var codeRegexp = regexp.MustCompile(`(VMM|CTGRS)-(\d+)`)

// vmmCodeClass maps VMM-xxxxx numeric codes (keyed by the numeric suffix as a
// string) to their charter classification. Populated from
// "CAPX Enterprise Readiness - Charter-2.pdf".
//
// Codes absent from this table fall through to HTTP-status classification.
var vmmCodeClass = buildVMMCodeClass()

// ctgrsCodeClass maps CTGRS-xxxxx numeric codes (keyed by the numeric suffix as
// a string) to their charter classification. Populated from
// "CAPX Enterprise Readiness - Charter.pdf".
var ctgrsCodeClass = buildCTGRSCodeClass()

func buildCTGRSCodeClass() map[string]ErrorClass {
	m := map[string]ErrorClass{}

	// Accepted: the requested state is already true.
	for _, c := range []string{"50002", "50023", "50044"} {
		m[c] = ClassAccepted
	}

	// Retriable: the same machine can try again.
	for _, c := range []string{
		"50004", "50010", "50011", "50012", "50020",
		"50022", "50024", "50035", "50037", "50051",
	} {
		m[c] = ClassRetriable
	}

	// Terminal: the same request fails again.
	for _, c := range []string{
		"50001", "50003", "50005", "50006", "50007", "50008", "50009",
		"50016", "50017", "50018", "50019", "50021", "50025", "50026",
		"50028", "50029", "50032", "50033", "50034", "50036", "50038",
		"50039", "50040", "50041", "50042", "50043", "50046", "50047",
		"50048", "50049", "50050", "50052", "50053", "50054", "50055",
		"50056", "50057", "50058",
	} {
		m[c] = ClassTerminal
	}

	return m
}

func buildVMMCodeClass() map[string]ErrorClass {
	m := map[string]ErrorClass{}

	// Retriable error codes (Charter-2 section 1).
	for _, c := range []string{
		"10005",          // API_NOT_ENABLED (VMM still deploying / upgrade in progress)
		"32805",          // ETAG_MISMATCH
		"30117",          // FILE_FAULT_ERROR
		"34604",          // GC_PROFILE_ETAG_MISMATCH
		"10003",          // OPERATION_TIME_OUT_ERROR
		"34023",          // POLICY_ETAG_MISMATCH
		"34076", "34077", // POLICY_INTERNAL_ERROR
		"10011", "10012", // RETRYABLE_ERROR
		"27003", // TEMPLATE_PLACEMENT_POLICY_ETAG_MISMATCH
		"27006", // TEMPLATE_PLACEMENT_POLICY_INTERNAL_ERROR
		"30303", // VM_ETAG_MISMATCH
		"31118", // VM_NGT_AGENT_RESTARTED
		"31111", // VM_NGT_CONNECTION_ERROR
		"31106", // VM_NGT_UPGRADE_TIMEOUT
		"30620", // VM_NIC_INTERNAL_ERROR
		"30617", // VM_NIC_UNCAUGHT_EXCEPTION_ERROR
		"37006", // VM_PROFILE_ETAG_MISMATCH
		"31700", // VM_PROJECT_MIGRATE_IN_PROGRESS_ERROR
		"10002", // VM_SERVICE_UNAVAILABLE_ERROR
		"36001", // VM_STORAGE_CLUSTER_NOT_REACHABLE
	} {
		m[c] = ClassRetriable
	}

	// Accepted error/warning codes (Charter-2 section 2): considered success.
	acceptedVMM := []string{
		"30611", "30616", // ALREADY_EXISTS
		"34603", // GC_PROFILE_ETAG_NOT_MODIFIED
		// MIGRATE_VM_WARNING (26 codes)
		"32700", "32701", "32702", "32703", "32704", "32705", "32706",
		"32707", "32708", "32709", "32710", "32711", "32712", "32713",
		"32714", "32715", "32716", "32717", "32718", "32719", "32720",
		"32721", "32722", "32723", "32724", "32725",
		"34022", // POLICY_ETAG_NOT_MODIFIED
		"30101", // VM_ALREADY_EXISTS
		"30302", // VM_ETAG_NOT_MODIFIED
		"30801", // VM_GPU_ALREADY_EXISTS
		"31105", // VM_NGT_ALREADY_INSTALLED
		"31104", // VM_NGT_ALREADY_MOUNTED
		"31107", // VM_NGT_ALREADY_UPGRADED
		"37007", // VM_PROFILE_ETAG_NOT_MODIFIED
		"30901", // VM_SERIAL_PORT_INDEX_IN_USE
	}
	for _, c := range acceptedVMM {
		m[c] = ClassAccepted
	}

	// Terminal error codes (Charter-2 section 3): no retry.
	terminalVMM := []string{
		// API_VERSION_NOT_SUPPORTED
		"10006", "10007", "10008",
		// BULK_CLONE_ERROR
		"30162", "30163", "30166",
		// CLUSTER_INCAPABLE_FOR_SHARED_STORAGE
		"36009", "36010",
		// CLUSTER_NOT_CAPABLE
		"30130", "30131",
		// CONFLICT_NOT_FOUND
		"34078",
		// CREATE_NIC_ERROR
		"30602", "30603",
		// CREATE_VM_ERROR
		"30111", "30134",
		// DEPRECATION_ERROR
		"10009", "10010",
		// ENTITY_MISMATCH
		"30710", "32804", "32813", "32815",
		// ESXI_GUEST_TOOLS_UNAVAILABLE_ERROR
		"30120",
		// GC_PROFILE_CREATE_INVALID_ARGUMENT
		"34610",
		// GC_PROFILE_ETAG_INVALID
		"34602",
		// GC_PROFILE_ETAG_MISSING
		"34601",
		// GC_PROFILE_INVALID_ARGUMENT
		"34608", "34611", "34612", "34613", "34614",
		// GC_PROFILE_INVALID_PROFILE_UUID
		"34609",
		// GC_PROFILE_INVALID_REQUEST_ID_HEADER
		"34606",
		// GC_PROFILE_MISSING_REQUEST_ID_HEADER
		"34605",
		// GC_PROFILE_NOT_FOUND
		"34607",
		// GC_PROFILE_NOT_FOUND_ERROR
		"30141",
		// GC_PROFILE_UNAUTHORIZED_REQUEST
		"34600",
		// GENERATE_VM_CONSOLE_TOKEN_ERROR
		"31401",
		// GUEST_CUSTOMIZATION_ERROR
		"31500", "31501", "31502", "31503", "31504", "31505", "31506",
		"31507", "31508", "31509", "31510", "31511", "31512", "31513",
		"31514",
		// IMAGE_CHECKOUT_ERROR
		"31200",
		// IMAGE_NOT_FOUND_ERROR
		"31201",
		// INSUFFICIENT_RESOURCES_ERROR
		"30118", "30128", "30129",
		// INTERNAL_ERROR (VMM-10000 only; not in RETRYABLE_ERROR group)
		"10000",
		// INVALID_ARGUMENT
		"30607", "30608", "30609", "30610", "30702", "30703", "30704",
		"30715", "32811", "32819",
		// INVALID_CONFIGURATION_ERROR
		"30119",
		// MIGRATE_NIC_ERROR
		"30601",
		// MULTIPLE_STORAGE_CLUSTER_ERROR
		"36005",
		// NON_LOCAL_VM_RECOVERY_POINT_ERROR
		"32816",
		// NO_POLICY_COMPLIANT_CLUSTER_ERROR
		"34410", "34411", "34412", "34413", "34414",
		// NO_SCHEDULABLE_HOST_ERROR
		"34418",
		// NO_STORAGE_POLICY_COMPLIANT_CONTAINER_ERROR
		"34415",
		// NO_SUITABLE_CLUSTER_ERROR
		"30132", "34405", "34406",
		// PARTIALLY_CONFIGURED_VM
		"32818",
		// POLICY_ETAG_INVALID
		"34021",
		// POLICY_ETAG_MISSING
		"34020",
		// POLICY_INVALID_ARGUMENT
		"34061", "34063", "34065", "34070", "34071", "34072", "34073",
		"34074", "34075",
		// POLICY_INVALID_POLICY_UUID
		"34064",
		// POLICY_INVALID_REQUEST_ID_HEADER
		"34041",
		// POLICY_MISSING_REQUEST_ID_HEADER
		"34040",
		// POLICY_NOT_FOUND
		"34060",
		// POLICY_UNAUTHORIZED_REQUEST
		"34000", "34001",
		// RESOURCE_SHORTAGE_ERROR
		"31304", "31306", "34400", "34401", "34402", "34403", "34404",
		"34416", "34417",
		// SUBNET_ACCESS_DENIED_ERROR
		"30605",
		// SUBNET_NOT_FOUND_ERROR
		"30604", "30614",
		// TEMPLATE_PLACEMENT_POLICY_INVALID_CATEGORIES
		"27002",
		// TEMPLATE_PLACEMENT_POLICY_INVALID_INPUT
		"27004",
		// TEMPLATE_PLACEMENT_POLICY_INVALID_ODATA_QUERY
		"27005",
		// TEMPLATE_PLACEMENT_POLICY_NOT_AUTHORIZED
		"27001",
		// TEMPLATE_PLACEMENT_POLICY_NOT_FOUND
		"27000",
		// TRANSFER_FILES_TO_GUEST_* family
		"31607", "31608", "31603", "31605", "31601", "31602", "31606",
		"31612", "31609", "31611", "31610", "31604", "31600",
		// UNSUPPORTED_OPERATION
		"10001", "10004", "30104", "30115", "30116", "30127", "30133",
		"30142", "30143", "30144", "30145", "30148", "30153", "30154",
		"30155", "30156", "30157", "30158", "30161", "30164", "30165",
		"30168", "30170", "30171", "30172", "30173", "30174", "30705",
		"30706", "30707", "30708", "30711", "30712", "30713", "30716",
		"30720", "30721", "30722", "32801", "32802", "32803", "32817",
		"32820", "32821", "32822", "34079", "34080", "34081",
		// UNSUPPORTED_SPEC_FOR_SHARED_STORAGE
		"36007", "36008",
		// VG_NOT_FOUND_ERROR
		"30709",
		// VG_RECOVERY_POINT_NOT_FOUND_ERROR
		"32814",
		// VM_CDROM_NOT_FOUND
		"31000",
		// VM_CONSOLE_SETUP_PUBLIC_PRIVATE_KEYS_ERROR
		"31400",
		// VM_CONSOLE_TOKEN_INVALID_ERROR
		"31402",
		// VM_CONSOLE_TOKEN_NOT_FOUND_ERROR
		"31403",
		// VM_DEVICE_BUNDLE_INVALID_ARGUMENT
		"37105",
		// VM_DEVICE_BUNDLE_NOT_FOUND_ERROR
		"37100",
		// VM_DEVICE_BUNDLE_PROFILE_NOT_FOUND_ERROR
		"37101",
		// VM_DEVICE_BUNDLE_UNSUPPORTED_OPERATION
		"37102", "37103", "37104",
		// VM_DISK_DATA_SOURCE_PROJECT_MISMATCH_ERROR
		"31702",
		// VM_DISK_NOT_FOUND_ERROR
		"30700",
		// VM_DISK_STORAGE_CONTAINER_NOT_FOUND_ERROR
		"30701", "30714",
		// VM_EMPTY_CDROM_NOT_FOUND
		"31100",
		// VM_ETAG_INVALID
		"30301",
		// VM_ETAG_MISSING
		"30300",
		// VM_GPU_INSUFFICIENT_RESOURCES_ERROR
		"30804",
		// VM_GPU_NOT_FOUND
		"30800",
		// VM_GPU_UNSUPPORTED_OPERATION
		"30802", "30803", "30805", "30806", "30807",
		// VM_HOST_AFFINITY_POLICY_ENFORCEMENT_ERROR
		"34200",
		// VM_HOST_AFFINITY_POLICY_UNSUPPORTED_OPERATION_ERROR
		"34201", "34202",
		// VM_INVALID_ARGUMENT
		"30102", "30106", "30107", "30108", "30109", "30110", "30112",
		"30113", "30114", "30121", "30122", "30123", "30124", "30125",
		"30126", "30137", "30138", "30139", "30140", "30146", "30147",
		"30149", "30150", "30151", "30152", "30159", "30167", "30169",
		"30175", "30176", "30177", "30178", "30179", "30180", "30717",
		"30718", "30719", "30723",
		// VM_INVALID_POWER_STATE_ERROR
		"30105",
		// VM_INVALID_REQUEST_ID_HEADER
		"30401",
		// VM_INVALID_STATE_ERROR
		"30136",
		// VM_INVALID_VM_UUID
		"30103", "30160",
		// VM_MISSING_REQUEST_ID_HEADER
		"30400",
		// VM_NGT_CONFIG_NOT_FOUND
		"31102",
		// VM_NGT_INSTALL_ERROR
		"31108", "31109", "31110", "31112", "31113", "31114",
		// VM_NGT_MOUNT_UNSUPPORTED
		"31115",
		// VM_NGT_NOT_ENABLED
		"31103",
		// VM_NGT_NOT_INSTALLED
		"31101",
		// VM_NGT_NOT_SUPPORTED
		"31116",
		// VM_NGT_VERSION_INCOMPATIBLE
		"31117",
		// VM_NIC_ADDRESS_POOL_EXHAUSTED
		"30621",
		// VM_NIC_INUSE_ERROR
		"30619",
		// VM_NIC_INVALID_ARGUMENT_ERROR
		"30618",
		// VM_NIC_NOT_FOUND_ERROR
		"30600", "30622",
		// VM_NIC_PROFILE_NOT_FOUND
		"30626",
		// VM_NIC_TYPE_MISMATCH_ERROR
		"30615",
		// VM_NIC_UNSUPPORTED_OPERATION
		"30606", "30612", "30613", "30623", "30624", "30625", "30627",
		"30628", "30629", "30630", "30631", "30632", "30633", "30634",
		"30635", "30636", "30637", "30638", "30639", "30640", "30641",
		"30642", "30643", "30644", "30645", "30646", "30647", "30648",
		"30649", "30650", "30651", "30652", "30653", "30654", "30655",
		"30656", "30657", "30658", "30660", "30661", "30662",
		// VM_NOT_FOUND
		"30100",
		// VM_OWNER_NOT_FOUND
		"35000",
		// VM_PCIE_DEVICE_NOT_FOUND_ERROR
		"31300",
		// VM_PCIE_SYNC_REP_ERROR
		"31305",
		// VM_PCIE_UNSUPPORTED_OPERATION
		"31301", "31302", "31303",
		// VM_PROFILE_ETAG_INVALID
		"37005",
		// VM_PROFILE_ETAG_MISSING
		"37004",
		// VM_PROFILE_INVALID_ARGUMENT
		"37001", "37002", "37008", "37010", "37011", "37012", "37013",
		"37020", "37022", "37023", "37024", "37025", "37026", "37027",
		"37028", "37029", "37030", "37031", "37032", "37033", "37034",
		"37035", "37036", "37037",
		// VM_PROFILE_INVALID_NIC_PROFILE_TYPE
		"37015",
		// VM_PROFILE_NIC_PROFILE_NOT_FOUND
		"37014",
		// VM_PROFILE_NOT_FOUND
		"37000",
		// VM_PROFILE_UNAUTHORIZED_REQUEST
		"37003",
		// VM_PROFILE_UNSUPPORTED_OPERATION
		"37009", "37038",
		// VM_PROJECT_ASSOCIATION_CHECK_ERROR
		"31701",
		// VM_PROJECT_UPDATE_NOT_SUPPORTED_ERROR
		"31703",
		// VM_RECOVERY_POINT_NOT_FOUND_ERROR
		"32810", "32812",
		// VM_RESOURCE_GROUP_CHECK_ERROR
		"31800", "31802",
		// VM_RESOURCE_GROUP_MISMATCH_ERROR
		"31803",
		// VM_RESOURCE_GROUP_NOT_FOUND_ERROR
		"31801", "31804",
		// VM_SERIAL_PORT_INDEX_RESERVED
		"30902",
		// VM_SERIAL_PORT_NOT_FOUND
		"30900",
		// VM_STORAGE_CLUSTER_CONFIG_ERROR
		"36003", "36004",
		// VM_STORAGE_CLUSTER_MISMATCH_ERROR
		"36006",
		// VM_STORAGE_CONTAINER_MISMATCH_ERROR
		"36011",
		// VM_STORAGE_CONTAINER_NOT_FOUND
		"36002",
		// VM_STORAGE_CONTAINER_NOT_SHARED
		"36000",
		// VM_UNAUTHORIZED_REQUEST
		"30500", "30501",
	}
	for _, c := range terminalVMM {
		m[c] = ClassTerminal
	}

	// MIGRATE_VM_ERROR (terminal, 207 codes). CAPX does not drive migrate, so
	// no matrix rows exercise these, but they are included for completeness.
	for _, c := range migrateVMErrorCodes() {
		m[c] = ClassTerminal
	}

	return m
}

// migrateVMErrorCodes returns the 207 MIGRATE_VM_ERROR codes from Charter-2.
func migrateVMErrorCodes() []string {
	return []string{
		"32000", "32001", "32002", "32003", "32004", "32005", "32006", "32007",
		"32008", "32009", "32010", "32011", "32012", "32013", "32014", "32015",
		"32016", "32017", "32018", "32019", "32020", "32021", "32022", "32023",
		"32024", "32025", "32026", "32027", "32028", "32029", "32030", "32031",
		"32032", "32033", "32034", "32035", "32036", "32037", "32038", "32039",
		"32040", "32041", "32042", "32043", "32044", "32045", "32046", "32047",
		"32048", "32049", "32050", "32051", "32052", "32053", "32054", "32055",
		"32056", "32057", "32058", "32059", "32060", "32061", "32062", "32063",
		"32064", "32065", "32066", "32067", "32068", "32069", "32070", "32071",
		"32072", "32073", "32074", "32075", "32080", "32081", "32083", "32084",
		"32085", "32086", "32087", "32088", "32089", "32090", "32091", "32092",
		"32093", "32094", "32095", "32096", "32097", "32098", "32099", "32100",
		"32101", "32102", "32103", "32104", "32105", "32106", "32107", "32108",
		"32109", "32111", "32112", "32113", "32114", "32115", "32116", "32117",
		"32118", "32119", "32121", "32122", "32123", "32124", "32125", "32126",
		"32127", "32128", "32129", "32130", "32131", "32132", "32133", "32134",
		"32135", "32136", "32137", "32138", "32139", "32140", "32141", "32142",
		"32143", "32144", "32145", "32146", "32147", "32148", "32149", "32150",
		"32151", "32152", "32153", "32154", "32155", "32156", "32157", "32158",
		"32159", "32160", "32161", "32162", "32163", "32164", "32165", "32166",
		"32167", "32168", "32169", "32170", "32171", "32172", "32173", "32174",
		"32175", "32176", "32177", "32200", "32201", "32202", "32210", "32211",
		"32212", "32213", "32300", "32301", "32305", "32306", "32307", "32308",
		"32309", "32310", "32311", "32312", "32313", "32314", "32330", "32331",
		"32332", "32333", "32334", "32335", "32336", "32600", "32601", "32602",
		"32603", "32604", "32605", "32606", "32607", "32608", "32609",
	}
}

// classFromCode extracts a charter error code from s and returns its class.
// Returns (ClassUnknown, false) if no known code is found.
func classFromCode(s string) (ErrorClass, bool) {
	if s == "" {
		return ClassUnknown, false
	}
	for _, match := range codeRegexp.FindAllStringSubmatch(s, -1) {
		prefix, num := match[1], match[2]
		var table map[string]ErrorClass
		switch prefix {
		case "VMM":
			table = vmmCodeClass
		case "CTGRS":
			table = ctgrsCodeClass
		}
		if class, ok := table[num]; ok {
			return class, true
		}
	}
	return ClassUnknown, false
}

// errorText gathers all text that may carry a charter code from an error: the
// error string itself, a wrapped *converged.APIError's Message, and the raw
// SDK response Body (where the numeric "code" field lives).
//
// Text after FailedSubtasksSuffix is omitted. That suffix is diagnostic child
// task text appended by the controller, and a code mentioned only there must
// not override the parent error's class.
func errorText(err error) string {
	var sb strings.Builder
	sb.WriteString(stripFailedSubtasks(err.Error()))

	var apiErr *converged.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Message != "" {
			sb.WriteByte(' ')
			sb.WriteString(apiErr.Message)
		}
		if body := bodyFromCause(apiErr.Cause); len(body) > 0 {
			sb.WriteByte(' ')
			sb.Write(body)
		}
	}
	return sb.String()
}

// stripFailedSubtasks drops the diagnostic child-task suffix so charter code
// lookup sees only the parent error.
func stripFailedSubtasks(s string) string {
	if i := strings.Index(s, FailedSubtasksSuffix); i >= 0 {
		return s[:i]
	}
	return s
}

// bodyFromCause extracts the raw SDK response Body ([]byte) from a
// GenericOpenAPIError-like cause using reflection, mirroring the SDK's own
// v4.GetStatusAndBody. The numeric charter "code" is carried in this body.
func bodyFromCause(cause error) []byte {
	if cause == nil {
		return nil
	}
	v := reflect.ValueOf(cause)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	if f := v.FieldByName("Body"); f.IsValid() && f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Uint8 {
		return f.Bytes()
	}
	return nil
}

// ClassifyError returns the charter ErrorClass for err.
//
// Resolution order:
//  1. Deterministic config errors (services.NewTerminalError) are terminal.
//  2. A VMM-xxxxx / CTGRS-xxxxx code found in the parent error message or SDK
//     response body is looked up in the charter tables. Codes that appear only
//     in the FailedSubtasksSuffix diagnostic trailer are ignored.
//  3. Otherwise fall back to the SDK's HTTP-status/errorGroup classification
//     (converged.IsNotFound / IsRateLimit / IsInternal).
//  4. A classified *converged.APIError with no matching code is terminal
//     (preserving the pre-existing "known APIError -> not retryable" behaviour);
//     anything else is treated as retriable via ClassUnknown.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ClassUnknown
	}

	// (1) Deterministic config errors are always terminal.
	if IsTerminalError(err) {
		return ClassTerminal
	}

	// (2) Charter error-code lookup.
	if class, ok := classFromCode(errorText(err)); ok {
		return class
	}

	// (3) HTTP-status / errorGroup fallback via the SDK sentinels.
	switch {
	case converged.IsNotFound(err):
		return ClassTerminal
	case converged.IsRateLimit(err), converged.IsInternal(err):
		return ClassRetriable
	}

	// (4) A classified APIError with no charter code and no retriable sentinel
	// is terminal; an unclassified error is unknown (retriable default).
	var apiErr *converged.APIError
	if errors.As(err, &apiErr) {
		return ClassTerminal
	}
	return ClassUnknown
}

// IsAcceptedError reports whether err is an "Accepted / error-ok" charter
// condition: the requested state is already satisfied (e.g. VM_ALREADY_EXISTS,
// CTGRS-50002/50023). Callers should treat such errors as success and proceed
// to GET/confirm-live.
func IsAcceptedError(err error) bool {
	return ClassifyError(err) == ClassAccepted
}
