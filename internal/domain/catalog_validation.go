package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidCatalog = errors.New("invalid catalog")

const maxCatalogBytes = 1048576

func invalidCatalogf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCatalog, fmt.Sprintf(format, args...))
}

func ValidateCatalog(c CatalogSnapshot) error {
	if c.SchemaVersion != 1 {
		return invalidCatalogf("unsupported schema version")
	}
	if c.Revision < 0 {
		return invalidCatalogf("negative revision")
	}
	if c.Projects == nil || c.Targets == nil || c.Environments == nil || c.Resources == nil || c.Bindings == nil || c.MissionLinks == nil {
		return invalidCatalogf("collections must be arrays")
	}
	if len(c.Projects) > 50 || len(c.Targets) > 250 || len(c.Environments) > 150 || len(c.Resources) > 500 || len(c.Bindings) > 1000 || len(c.MissionLinks) > 2000 {
		return invalidCatalogf("collection limit exceeded")
	}

	allIDs := make(map[string]struct{}, len(c.Projects)+len(c.Targets)+len(c.Environments)+len(c.Resources)+len(c.Bindings))
	projectIDs := make(map[string]struct{}, len(c.Projects))
	for i := range c.Projects {
		project := &c.Projects[i]
		if err := validateFinalMeta(project.CatalogMeta, allIDs); err != nil {
			return invalidCatalogf("project metadata: %v", err)
		}
		if err := validateName(project.Name); err != nil {
			return invalidCatalogf("project name: %v", err)
		}
		if err := validateDescription(project.Description); err != nil {
			return invalidCatalogf("project description: %v", err)
		}
		projectIDs[project.ID] = struct{}{}
	}

	for i := range c.Targets {
		target := &c.Targets[i]
		if err := validateFinalMeta(target.CatalogMeta, allIDs); err != nil {
			return invalidCatalogf("target metadata: %v", err)
		}
		if err := validateReferenceID(target.ProjectID); err != nil {
			return invalidCatalogf("target project: %v", err)
		}
		if _, ok := projectIDs[target.ProjectID]; !ok {
			return invalidCatalogf("target project does not exist")
		}
		if err := validateName(target.Name); err != nil {
			return invalidCatalogf("target name: %v", err)
		}
		if !validTarget(target.Platform, target.Format, target.Architecture, target.Distribution) {
			return invalidCatalogf("unsupported target combination")
		}
	}

	environmentProjects := make(map[string]string, len(c.Environments))
	for i := range c.Environments {
		environment := &c.Environments[i]
		if err := validateFinalMeta(environment.CatalogMeta, allIDs); err != nil {
			return invalidCatalogf("environment metadata: %v", err)
		}
		if err := validateReferenceID(environment.ProjectID); err != nil {
			return invalidCatalogf("environment project: %v", err)
		}
		if _, ok := projectIDs[environment.ProjectID]; !ok {
			return invalidCatalogf("environment project does not exist")
		}
		if err := validateName(environment.Name); err != nil {
			return invalidCatalogf("environment name: %v", err)
		}
		if !oneOf(environment.Tier, "local", "dev", "staging", "production") {
			return invalidCatalogf("unsupported environment tier")
		}
		environmentProjects[environment.ID] = environment.ProjectID
	}

	resourceIDs := make(map[string]struct{}, len(c.Resources))
	resourceIdentities := make(map[string]struct{}, len(c.Resources))
	for i := range c.Resources {
		resource := &c.Resources[i]
		if err := validateFinalMeta(resource.CatalogMeta, allIDs); err != nil {
			return invalidCatalogf("resource metadata: %v", err)
		}
		if err := validateName(resource.Name); err != nil {
			return invalidCatalogf("resource name: %v", err)
		}
		if !oneOf(resource.Kind, "service", "database", "storage", "domain", "runner") {
			return invalidCatalogf("unsupported resource kind")
		}
		if resource.OwnerProjectID != nil {
			if err := validateReferenceID(*resource.OwnerProjectID); err != nil {
				return invalidCatalogf("resource owner: %v", err)
			}
			if _, ok := projectIDs[*resource.OwnerProjectID]; !ok {
				return invalidCatalogf("resource owner does not exist")
			}
		}
		if !oneOf(resource.Provider, "manual", "local", "aws", "gcp", "azure", "apple") {
			return invalidCatalogf("unsupported resource provider")
		}
		if resource.Provider == "manual" {
			if resource.Account != "" || resource.Region != "" || resource.ExternalID != "" {
				return invalidCatalogf("manual resource must not have an external identity")
			}
		} else {
			if err := validateIdentityPart(resource.Account); err != nil {
				return invalidCatalogf("resource account: %v", err)
			}
			if err := validateIdentityPart(resource.Region); err != nil {
				return invalidCatalogf("resource region: %v", err)
			}
			if err := validateIdentityPart(resource.ExternalID); err != nil {
				return invalidCatalogf("resource external identity: %v", err)
			}
			identity := resource.Provider + "\x00" + resource.Account + "\x00" + resource.Region + "\x00" + resource.ExternalID
			if _, exists := resourceIdentities[identity]; exists {
				return invalidCatalogf("duplicate resource identity")
			}
			resourceIdentities[identity] = struct{}{}
		}
		if !resource.Protected || resource.Retention != "retain" {
			return invalidCatalogf("resource protection policy is invalid")
		}
		resourceIDs[resource.ID] = struct{}{}
	}

	bindingSlots := make(map[string]struct{}, len(c.Bindings))
	for i := range c.Bindings {
		binding := &c.Bindings[i]
		if err := validateFinalMeta(binding.CatalogMeta, allIDs); err != nil {
			return invalidCatalogf("binding metadata: %v", err)
		}
		if err := validateReferenceID(binding.ProjectID); err != nil {
			return invalidCatalogf("binding project: %v", err)
		}
		if err := validateReferenceID(binding.EnvironmentID); err != nil {
			return invalidCatalogf("binding environment: %v", err)
		}
		if err := validateReferenceID(binding.ResourceID); err != nil {
			return invalidCatalogf("binding resource: %v", err)
		}
		if _, ok := projectIDs[binding.ProjectID]; !ok {
			return invalidCatalogf("binding project does not exist")
		}
		environmentProject, ok := environmentProjects[binding.EnvironmentID]
		if !ok {
			return invalidCatalogf("binding environment does not exist")
		}
		if environmentProject != binding.ProjectID {
			return invalidCatalogf("binding environment belongs to another project")
		}
		if _, ok := resourceIDs[binding.ResourceID]; !ok {
			return invalidCatalogf("binding resource does not exist")
		}
		if err := validatePurpose(binding.Purpose); err != nil {
			return invalidCatalogf("binding purpose: %v", err)
		}
		slot := binding.ProjectID + "\x00" + binding.EnvironmentID + "\x00" + binding.Purpose
		if _, exists := bindingSlots[slot]; exists {
			return invalidCatalogf("duplicate binding purpose")
		}
		bindingSlots[slot] = struct{}{}
	}

	missionIDs := make(map[string]struct{}, len(c.MissionLinks))
	for i := range c.MissionLinks {
		link := &c.MissionLinks[i]
		if err := validateReferenceID(link.MissionID); err != nil {
			return invalidCatalogf("mission link mission: %v", err)
		}
		if err := validateReferenceID(link.ProjectID); err != nil {
			return invalidCatalogf("mission link project: %v", err)
		}
		if _, ok := projectIDs[link.ProjectID]; !ok {
			return invalidCatalogf("mission link project does not exist")
		}
		if _, exists := missionIDs[link.MissionID]; exists {
			return invalidCatalogf("duplicate mission link")
		}
		missionIDs[link.MissionID] = struct{}{}
		if err := validateUTC(link.LinkedAt); err != nil {
			return invalidCatalogf("mission link time: %v", err)
		}
	}

	encoded, err := json.Marshal(c)
	if err != nil {
		return invalidCatalogf("catalog is not JSON encodable")
	}
	if len(encoded) > maxCatalogBytes {
		return invalidCatalogf("encoded catalog is too large")
	}
	return nil
}

func ValidateCatalogChange(before, after CatalogSnapshot, kind string) error {
	if !oneOf(kind,
		"project.create", "project.update",
		"target.create", "target.rename",
		"environment.create", "environment.rename",
		"resource.create", "resource.rename", "resource.bind", "resource.unbind",
		"mission.link", "mission.relink", "mission.unlink",
	) {
		return invalidCatalogf("unsupported catalog change kind")
	}
	if err := ValidateCatalog(before); err != nil {
		return invalidCatalogf("before snapshot is invalid")
	}
	if before.SchemaVersion != after.SchemaVersion || before.Revision != after.Revision {
		return invalidCatalogf("snapshot metadata changed before stamping")
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}

	var err error
	switch kind {
	case "project.create":
		err = validateProjectCreate(before, after)
	case "project.update":
		err = validateProjectUpdate(before, after)
	case "target.create":
		err = validateTargetCreate(before, after)
	case "target.rename":
		err = validateTargetRename(before, after)
	case "environment.create":
		err = validateEnvironmentCreate(before, after)
	case "environment.rename":
		err = validateEnvironmentRename(before, after)
	case "resource.create":
		err = validateResourceCreate(before, after)
	case "resource.rename":
		err = validateResourceRename(before, after)
	case "resource.bind":
		err = validateResourceBind(before, after)
	case "resource.unbind":
		err = validateResourceUnbind(before, after)
	case "mission.link":
		err = validateMissionLink(before, after)
	case "mission.relink":
		err = validateMissionRelink(before, after)
	case "mission.unlink":
		err = validateMissionUnlink(before, after)
	}
	if err != nil {
		return invalidCatalogf("change is not permitted for %s", kind)
	}
	return nil
}

func validateProjectCreate(before, after CatalogSnapshot) error {
	if !sameExceptProjects(before, after) || len(after.Projects) != len(before.Projects)+1 || !reflect.DeepEqual(before.Projects, after.Projects[:len(before.Projects)]) {
		return errors.New("unexpected change")
	}
	return validateNewMeta(after.Projects[len(before.Projects)].CatalogMeta, before)
}

func validateProjectUpdate(before, after CatalogSnapshot) error {
	if !sameExceptProjects(before, after) || len(before.Projects) != len(after.Projects) {
		return errors.New("unexpected change")
	}
	changes := 0
	for i := range before.Projects {
		if reflect.DeepEqual(before.Projects[i], after.Projects[i]) {
			continue
		}
		changes++
		if before.Projects[i].CatalogMeta != after.Projects[i].CatalogMeta {
			return errors.New("metadata changed")
		}
	}
	if changes != 1 {
		return errors.New("wrong number of changes")
	}
	return nil
}

func validateTargetCreate(before, after CatalogSnapshot) error {
	if !sameExceptTargets(before, after) || len(after.Targets) != len(before.Targets)+1 || !reflect.DeepEqual(before.Targets, after.Targets[:len(before.Targets)]) {
		return errors.New("unexpected change")
	}
	created := after.Targets[len(before.Targets)]
	if err := validateNewMeta(created.CatalogMeta, before); err != nil {
		return err
	}
	return validateReferenceID(created.ProjectID)
}

func validateTargetRename(before, after CatalogSnapshot) error {
	if !sameExceptTargets(before, after) || len(before.Targets) != len(after.Targets) {
		return errors.New("unexpected change")
	}
	changes := 0
	for i := range before.Targets {
		if reflect.DeepEqual(before.Targets[i], after.Targets[i]) {
			continue
		}
		changes++
		normalized := after.Targets[i]
		normalized.Name = before.Targets[i].Name
		if !reflect.DeepEqual(before.Targets[i], normalized) {
			return errors.New("immutable target field changed")
		}
	}
	if changes != 1 {
		return errors.New("wrong number of changes")
	}
	return nil
}

func validateEnvironmentCreate(before, after CatalogSnapshot) error {
	if !sameExceptEnvironments(before, after) || len(after.Environments) != len(before.Environments)+1 || !reflect.DeepEqual(before.Environments, after.Environments[:len(before.Environments)]) {
		return errors.New("unexpected change")
	}
	created := after.Environments[len(before.Environments)]
	if err := validateNewMeta(created.CatalogMeta, before); err != nil {
		return err
	}
	return validateReferenceID(created.ProjectID)
}

func validateEnvironmentRename(before, after CatalogSnapshot) error {
	if !sameExceptEnvironments(before, after) || len(before.Environments) != len(after.Environments) {
		return errors.New("unexpected change")
	}
	changes := 0
	for i := range before.Environments {
		if reflect.DeepEqual(before.Environments[i], after.Environments[i]) {
			continue
		}
		changes++
		normalized := after.Environments[i]
		normalized.Name = before.Environments[i].Name
		if !reflect.DeepEqual(before.Environments[i], normalized) {
			return errors.New("immutable environment field changed")
		}
	}
	if changes != 1 {
		return errors.New("wrong number of changes")
	}
	return nil
}

func validateResourceCreate(before, after CatalogSnapshot) error {
	if !sameExceptResources(before, after) || len(after.Resources) != len(before.Resources)+1 || !reflect.DeepEqual(before.Resources, after.Resources[:len(before.Resources)]) {
		return errors.New("unexpected change")
	}
	created := after.Resources[len(before.Resources)]
	if err := validateNewMeta(created.CatalogMeta, before); err != nil {
		return err
	}
	if created.OwnerProjectID != nil {
		return validateReferenceID(*created.OwnerProjectID)
	}
	return nil
}

func validateResourceRename(before, after CatalogSnapshot) error {
	if !sameExceptResources(before, after) || len(before.Resources) != len(after.Resources) {
		return errors.New("unexpected change")
	}
	changes := 0
	for i := range before.Resources {
		if reflect.DeepEqual(before.Resources[i], after.Resources[i]) {
			continue
		}
		changes++
		normalized := after.Resources[i]
		normalized.Name = before.Resources[i].Name
		if !reflect.DeepEqual(before.Resources[i], normalized) {
			return errors.New("immutable resource field changed")
		}
	}
	if changes != 1 {
		return errors.New("wrong number of changes")
	}
	return nil
}

func validateResourceBind(before, after CatalogSnapshot) error {
	if !sameExceptBindings(before, after) || len(after.Bindings) != len(before.Bindings)+1 || !reflect.DeepEqual(before.Bindings, after.Bindings[:len(before.Bindings)]) {
		return errors.New("unexpected change")
	}
	created := after.Bindings[len(before.Bindings)]
	if err := validateNewMeta(created.CatalogMeta, before); err != nil {
		return err
	}
	if err := validateReferenceID(created.ProjectID); err != nil {
		return err
	}
	if err := validateReferenceID(created.EnvironmentID); err != nil {
		return err
	}
	return validateReferenceID(created.ResourceID)
}

func validateResourceUnbind(before, after CatalogSnapshot) error {
	if !sameExceptBindings(before, after) || !removedOne(before.Bindings, after.Bindings) {
		return errors.New("unexpected change")
	}
	return nil
}

func validateMissionLink(before, after CatalogSnapshot) error {
	if !sameExceptMissionLinks(before, after) || len(after.MissionLinks) != len(before.MissionLinks)+1 || !reflect.DeepEqual(before.MissionLinks, after.MissionLinks[:len(before.MissionLinks)]) {
		return errors.New("unexpected change")
	}
	created := after.MissionLinks[len(before.MissionLinks)]
	if created.LinkedAt != "" {
		return errors.New("new link was stamped early")
	}
	if err := validateReferenceID(created.MissionID); err != nil {
		return err
	}
	if err := validateReferenceID(created.ProjectID); err != nil {
		return err
	}
	for _, existing := range before.MissionLinks {
		if existing.MissionID == created.MissionID {
			return errors.New("mission is already linked")
		}
	}
	return nil
}

func validateMissionRelink(before, after CatalogSnapshot) error {
	if !sameExceptMissionLinks(before, after) || len(before.MissionLinks) != len(after.MissionLinks) {
		return errors.New("unexpected change")
	}
	changes := 0
	for i := range before.MissionLinks {
		if reflect.DeepEqual(before.MissionLinks[i], after.MissionLinks[i]) {
			continue
		}
		changes++
		if before.MissionLinks[i].MissionID != after.MissionLinks[i].MissionID || before.MissionLinks[i].ProjectID == after.MissionLinks[i].ProjectID || after.MissionLinks[i].LinkedAt != "" {
			return errors.New("invalid relink")
		}
		if err := validateReferenceID(after.MissionLinks[i].ProjectID); err != nil {
			return err
		}
	}
	if changes != 1 {
		return errors.New("wrong number of changes")
	}
	return nil
}

func validateMissionUnlink(before, after CatalogSnapshot) error {
	if !sameExceptMissionLinks(before, after) || !removedOne(before.MissionLinks, after.MissionLinks) {
		return errors.New("unexpected change")
	}
	return nil
}

func validateNewMeta(meta CatalogMeta, before CatalogSnapshot) error {
	if err := validateReferenceID(meta.ID); err != nil {
		return err
	}
	if meta.Revision != 0 || meta.CreatedAt != "" || meta.UpdatedAt != "" {
		return errors.New("new metadata was stamped early")
	}
	if catalogHasEntityID(before, meta.ID) {
		return errors.New("entity ID already exists")
	}
	return nil
}

func catalogHasEntityID(c CatalogSnapshot, id string) bool {
	for _, project := range c.Projects {
		if project.ID == id {
			return true
		}
	}
	for _, target := range c.Targets {
		if target.ID == id {
			return true
		}
	}
	for _, environment := range c.Environments {
		if environment.ID == id {
			return true
		}
	}
	for _, resource := range c.Resources {
		if resource.ID == id {
			return true
		}
	}
	for _, binding := range c.Bindings {
		if binding.ID == id {
			return true
		}
	}
	return false
}

func sameExceptProjects(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Targets, after.Targets) &&
		reflect.DeepEqual(before.Environments, after.Environments) &&
		reflect.DeepEqual(before.Resources, after.Resources) &&
		reflect.DeepEqual(before.Bindings, after.Bindings) &&
		reflect.DeepEqual(before.MissionLinks, after.MissionLinks)
}

func sameExceptTargets(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Projects, after.Projects) &&
		reflect.DeepEqual(before.Environments, after.Environments) &&
		reflect.DeepEqual(before.Resources, after.Resources) &&
		reflect.DeepEqual(before.Bindings, after.Bindings) &&
		reflect.DeepEqual(before.MissionLinks, after.MissionLinks)
}

func sameExceptEnvironments(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Projects, after.Projects) &&
		reflect.DeepEqual(before.Targets, after.Targets) &&
		reflect.DeepEqual(before.Resources, after.Resources) &&
		reflect.DeepEqual(before.Bindings, after.Bindings) &&
		reflect.DeepEqual(before.MissionLinks, after.MissionLinks)
}

func sameExceptResources(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Projects, after.Projects) &&
		reflect.DeepEqual(before.Targets, after.Targets) &&
		reflect.DeepEqual(before.Environments, after.Environments) &&
		reflect.DeepEqual(before.Bindings, after.Bindings) &&
		reflect.DeepEqual(before.MissionLinks, after.MissionLinks)
}

func sameExceptBindings(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Projects, after.Projects) &&
		reflect.DeepEqual(before.Targets, after.Targets) &&
		reflect.DeepEqual(before.Environments, after.Environments) &&
		reflect.DeepEqual(before.Resources, after.Resources) &&
		reflect.DeepEqual(before.MissionLinks, after.MissionLinks)
}

func sameExceptMissionLinks(before, after CatalogSnapshot) bool {
	return reflect.DeepEqual(before.Projects, after.Projects) &&
		reflect.DeepEqual(before.Targets, after.Targets) &&
		reflect.DeepEqual(before.Environments, after.Environments) &&
		reflect.DeepEqual(before.Resources, after.Resources) &&
		reflect.DeepEqual(before.Bindings, after.Bindings)
}

func removedOne[T any](before, after []T) bool {
	if len(after) != len(before)-1 {
		return false
	}
	for removed := range before {
		candidate := append([]T{}, before[:removed]...)
		candidate = append(candidate, before[removed+1:]...)
		if reflect.DeepEqual(candidate, after) {
			return true
		}
	}
	return false
}

func validateFinalMeta(meta CatalogMeta, allIDs map[string]struct{}) error {
	if err := validateReferenceID(meta.ID); err != nil {
		return err
	}
	if _, exists := allIDs[meta.ID]; exists {
		return fmt.Errorf("duplicate entity ID")
	}
	allIDs[meta.ID] = struct{}{}
	if meta.Revision < 1 {
		return fmt.Errorf("revision must be positive")
	}
	if err := validateUTC(meta.CreatedAt); err != nil {
		return fmt.Errorf("created time: %v", err)
	}
	if err := validateUTC(meta.UpdatedAt); err != nil {
		return fmt.Errorf("updated time: %v", err)
	}
	return nil
}

func validateUTC(value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("must be RFC3339Nano")
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return fmt.Errorf("must be UTC")
	}
	return nil
}

func validateReferenceID(value string) error {
	if len(value) == 0 || len(value) > 64 {
		return fmt.Errorf("ID length is invalid")
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-') {
			return fmt.Errorf("ID contains an invalid character")
		}
	}
	return nil
}

func validateName(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("must be valid UTF-8")
	}
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	if length < 1 || length > 120 {
		return fmt.Errorf("length is invalid")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("contains a control character")
		}
	}
	return nil
}

func validateDescription(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > 2000 {
		return fmt.Errorf("is too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' {
			return fmt.Errorf("contains a control character")
		}
	}
	return nil
}

func validateIdentityPart(value string) error {
	if len(value) == 0 || len(value) > 256 || !utf8.ValidString(value) || strings.Contains(value, "://") {
		return fmt.Errorf("value is invalid")
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("_-.:/", rune(b))) {
			return fmt.Errorf("value contains an invalid character")
		}
	}
	return nil
}

func validatePurpose(value string) error {
	if len(value) == 0 || len(value) > 64 {
		return fmt.Errorf("length is invalid")
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if i == 0 {
			if !((b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')) {
				return fmt.Errorf("first character is invalid")
			}
		} else if !((b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-') {
			return fmt.Errorf("contains an invalid character")
		}
	}
	return nil
}

func validTarget(platform, format, architecture, distribution string) bool {
	switch platform {
	case "windows":
		return format == "exe" && oneOf(architecture, "x64", "arm64") && distribution == "direct"
	case "macos":
		return format == "dmg" && oneOf(architecture, "x64", "arm64", "universal") && distribution == "direct"
	case "android":
		return format == "apk" && oneOf(architecture, "arm64", "x86_64", "universal") && distribution == "direct"
	case "ios":
		return format == "ipa" && architecture == "arm64" && oneOf(distribution, "development", "ad_hoc", "testflight", "app_store")
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
