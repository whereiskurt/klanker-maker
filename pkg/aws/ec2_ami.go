// Package aws — ec2_ami.go
// AMI lifecycle helpers: Bake, List, Delete, Copy.
//
// All functions accept a narrow EC2AMIAPI interface so callers can inject mocks
// in tests without spinning up real AWS infrastructure.
package aws

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2AMIAPI is the narrow EC2 interface for AMI lifecycle operations.
// The five methods here cover all operations needed by the Wave 2 consumers
// (km ami list/delete/bake/copy and km doctor checkStaleAMIs).
// Implemented by *ec2.Client.
type EC2AMIAPI interface {
	CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error)
	CopyImage(ctx context.Context, params *ec2.CopyImageInput, optFns ...func(*ec2.Options)) (*ec2.CopyImageOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// amiNameSanitizer replaces characters that are illegal in AMI Name fields.
// AWS allows: alphanumeric, ()[]/ .-_'@  — anything else is replaced with '-'.
var amiNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._/ \-\[\]()'@]`)

// AMIName returns an AMI Name value in the format:
//
//	km-{sanitized-profile}-{sandboxID}-{YYYYMMDDHHMMSS}
//
// The sanitizer replaces illegal characters with '-' and the total output is
// capped at 128 characters (AWS maximum). If profileName is empty the literal
// placeholder "profile" is used.
func AMIName(profileName, sandboxID string, t time.Time) string {
	if profileName == "" {
		profileName = "profile"
	}
	safe := amiNameSanitizer.ReplaceAllString(profileName, "-")

	// Suffix is fixed-width: "-" + sandboxID + "-" + 14-digit timestamp.
	suffix := fmt.Sprintf("-%s-%s", sandboxID, t.UTC().Format("20060102150405"))

	// Prefix is "km-".
	prefix := "km-"

	maxSafe := 128 - len(prefix) - len(suffix)
	if maxSafe < 0 {
		maxSafe = 0
	}
	if len(safe) > maxSafe {
		safe = safe[:maxSafe]
	}

	return prefix + safe + suffix
}

// KMBakeTags returns the standard tag set applied to a km-baked AMI and its
// associated EBS snapshots. Pass these directly to BakeAMI via the tags
// parameter so they are applied atomically in the CreateImage call.
//
// The kmVersion parameter should be the operator-side km binary version string
// (e.g. "v1.2.3"); inject it at the call site to avoid coupling to a global.
//
// If alias is empty the km:alias tag is omitted from the returned slice.
func KMBakeTags(sandboxID, profileName, alias, instanceType, sourceRegion, kmVersion string) []types.Tag {
	tags := []types.Tag{
		{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String(sandboxID)},
		{Key: awssdk.String("km:profile"), Value: awssdk.String(profileName)},
		{Key: awssdk.String("km:baked-at"), Value: awssdk.String(time.Now().UTC().Format(time.RFC3339))},
		{Key: awssdk.String("km:source-region"), Value: awssdk.String(sourceRegion)},
		{Key: awssdk.String("km:instance-type"), Value: awssdk.String(instanceType)},
		{Key: awssdk.String("km:baked-by"), Value: awssdk.String("km")},
		{Key: awssdk.String("km:km-version"), Value: awssdk.String(kmVersion)},
		{Key: awssdk.String("Name"), Value: awssdk.String(AMIName(profileName, sandboxID, time.Now().UTC()))},
	}
	if alias != "" {
		tags = append(tags, types.Tag{
			Key:   awssdk.String("km:alias"),
			Value: awssdk.String(alias),
		})
	}
	return tags
}

// describeImagesClient is a thin adapter so EC2AMIAPI values can be passed to
// ec2.NewImageAvailableWaiter, which requires ec2.DescribeImagesAPIClient.
// Because EC2AMIAPI.DescribeImages has the identical signature the compiler
// accepts the assignment without a runtime cast.
type describeImagesClient struct{ api EC2AMIAPI }

func (d describeImagesClient) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return d.api.DescribeImages(ctx, params, optFns...)
}

// BakeAMI creates an AMI from a running EC2 instance using NoReboot=true (live
// snapshot). Tags are applied atomically to both the image and its underlying
// EBS snapshots via TagSpecifications. The function waits until the AMI reaches
// the "available" state before returning.
//
// If waitTimeout is zero the default of 15 minutes is used.
//
// On a CreateImage error the function returns ("", wrappedErr).
// On a waiter timeout it returns (imageID, wrappedErr) so the caller knows the
// AMI ID and can decide whether to clean up.
func BakeAMI(ctx context.Context, client EC2AMIAPI, instanceID, amiName, description string, tags []types.Tag, waitTimeout time.Duration) (string, error) {
	if waitTimeout == 0 {
		waitTimeout = 15 * time.Minute
	}

	tagSpecs := []types.TagSpecification{
		{ResourceType: types.ResourceTypeImage, Tags: tags},
		{ResourceType: types.ResourceTypeSnapshot, Tags: tags},
	}

	out, err := client.CreateImage(ctx, &ec2.CreateImageInput{
		InstanceId:        awssdk.String(instanceID),
		Name:              awssdk.String(amiName),
		Description:       awssdk.String(description),
		NoReboot:          awssdk.Bool(true),
		TagSpecifications: tagSpecs,
	})
	if err != nil {
		return "", fmt.Errorf("create image %s: %w", instanceID, err)
	}

	amiID := awssdk.ToString(out.ImageId)
	fmt.Fprintf(os.Stderr, "[ami] snapshot started: %s (waiting for available state...)\n", amiID)

	waiter := ec2.NewImageAvailableWaiter(describeImagesClient{client})
	if err := waiter.Wait(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{amiID},
	}, waitTimeout); err != nil {
		return amiID, fmt.Errorf("create image %s: wait available: %w", instanceID, err)
	}

	return amiID, nil
}

// AMIBDMDeviceNames returns the device names from BlockDeviceMappings for an AMI.
// Returns (nil, nil) when amiID is empty or no images are returned.
// Used by the compiler to detect /dev/sdf collision before emitting additionalVolume HCL.
func AMIBDMDeviceNames(ctx context.Context, client EC2AMIAPI, amiID string) ([]string, error) {
	if amiID == "" {
		return nil, nil
	}
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{amiID}})
	if err != nil {
		return nil, fmt.Errorf("describe images %s for BDM: %w", amiID, err)
	}
	if len(out.Images) == 0 {
		return nil, nil
	}
	var devices []string
	for _, bdm := range out.Images[0].BlockDeviceMappings {
		if bdm.DeviceName != nil {
			devices = append(devices, *bdm.DeviceName)
		}
	}
	return devices, nil
}

// ListBakedAMIs returns all self-owned AMIs that carry a km:sandbox-id tag and
// are in the "available" state. Results are sorted newest-first by CreationDate.
// DescribeImages is not a paginated API for self-owned images; no NextToken loop
// is needed.
func ListBakedAMIs(ctx context.Context, client EC2AMIAPI) ([]types.Image, error) {
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"self"},
		Filters: []types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{"*"}},
			{Name: awssdk.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list baked AMIs: %w", err)
	}

	images := out.Images
	sort.Slice(images, func(i, j int) bool {
		ci := awssdk.ToString(images[i].CreationDate)
		cj := awssdk.ToString(images[j].CreationDate)
		return ci > cj // descending: newer strings sort later but > gives newest first
	})
	return images, nil
}

// DeleteAMI deregisters an AMI and atomically deletes its associated EBS
// snapshots. If the AMI shares a snapshot with another image, AWS silently
// skips that snapshot (expected behavior, not an error).
//
// Pass dryRun=true to validate permissions without making state changes.
func DeleteAMI(ctx context.Context, client EC2AMIAPI, amiID string, dryRun bool) error {
	_, err := client.DeregisterImage(ctx, &ec2.DeregisterImageInput{
		ImageId:                   awssdk.String(amiID),
		DeleteAssociatedSnapshots: awssdk.Bool(true),
		DryRun:                    awssdk.Bool(dryRun),
	})
	if err != nil {
		return fmt.Errorf("deregister image %s: %w", amiID, err)
	}
	return nil
}

// SnapshotIDsFromImage returns the EBS snapshot IDs for every block device
// mapping in img that has an EBS volume. Used by dry-run previews to show which
// snapshots would be deleted alongside the AMI.
func SnapshotIDsFromImage(img types.Image) []string {
	var ids []string
	for _, bdm := range img.BlockDeviceMappings {
		if bdm.Ebs != nil && bdm.Ebs.SnapshotId != nil {
			ids = append(ids, *bdm.Ebs.SnapshotId)
		}
	}
	return ids
}

// CopyAMI copies an AMI to a destination region and re-tags the new image (and
// its snapshots) because AWS CopyImage does not inherit tags. It waits until the
// destination AMI is available before returning.
//
// srcClient and dstClient are EC2 clients pre-configured for the source and
// destination regions respectively. tags is the complete tag set to apply to the
// new AMI and its snapshots in the destination region.
//
// If waitTimeout is zero the default of 15 minutes is used.
//
// On success returns (dstAMIID, nil).
// On post-copy failures (tagging, waiter) returns (dstAMIID, wrappedErr) so
// the caller knows the AMI exists but may be in a partial state.
func CopyAMI(ctx context.Context, srcClient, dstClient EC2AMIAPI, srcRegion, dstRegion, srcAMIID, name, description string, tags []types.Tag, waitTimeout time.Duration) (string, error) {
	if waitTimeout == 0 {
		waitTimeout = 15 * time.Minute
	}

	copyOut, err := dstClient.CopyImage(ctx, &ec2.CopyImageInput{
		SourceImageId: awssdk.String(srcAMIID),
		SourceRegion:  awssdk.String(srcRegion),
		Name:          awssdk.String(name),
		Description:   awssdk.String(description),
	})
	if err != nil {
		return "", fmt.Errorf("copy image %s to %s: %w", srcAMIID, dstRegion, err)
	}

	dstAMIID := awssdk.ToString(copyOut.ImageId)
	fmt.Fprintf(os.Stderr, "[ami] copy started: %s → %s in %s (waiting for available state...)\n", srcAMIID, dstAMIID, dstRegion)

	waiter := ec2.NewImageAvailableWaiter(describeImagesClient{dstClient})
	if err := waiter.Wait(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{dstAMIID},
	}, waitTimeout); err != nil {
		return dstAMIID, fmt.Errorf("copy image %s: wait available in %s: %w", srcAMIID, dstRegion, err)
	}

	// Discover the new snapshot IDs in the destination region so we can tag them
	// too. CopyImage does not inherit tags (Pitfall 3 from RESEARCH.md).
	descOut, err := dstClient.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{dstAMIID},
	})
	if err != nil {
		return dstAMIID, fmt.Errorf("copy image %s: describe destination %s: %w", srcAMIID, dstAMIID, err)
	}

	// Build the resource list: destination AMI ID + each snapshot ID.
	resources := []string{dstAMIID}
	if len(descOut.Images) > 0 {
		for _, id := range SnapshotIDsFromImage(descOut.Images[0]) {
			resources = append(resources, id)
		}
	}

	if _, err := dstClient.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: resources,
		Tags:      tags,
	}); err != nil {
		return dstAMIID, fmt.Errorf("copy image %s: tag destination resources: %w", srcAMIID, err)
	}

	return dstAMIID, nil
}
