# AWS VPC Source Specification Delta

## Purpose

Reports the address blocks in use in one AWS region's VPCs, so that a space
planned for AWS can be checked and allocated from without a write permission.

## ADDED Requirements

### Requirement: Settings select what is read

The `aws-vpc` source SHALL require `region` and accept `vpc` (one VPC ID),
`resource` (`vpc`, the default, or `subnet`), and `profile` (the name of a shared
configuration profile). It SHALL accept no other setting.

#### Scenario: A missing region

- **WHEN** `source add prod use1 --type aws-vpc` runs
- **THEN** it exits 2 and names `region`

#### Scenario: An unknown resource

- **WHEN** `--set resource=eni` is given
- **THEN** it exits 2 as invalid input

### Requirement: A VPC setting narrows the evidence

When `vpc` is set, the source SHALL report only that VPC's blocks, or only its
subnets with `resource=subnet`. It SHALL fail when that VPC is not found.

#### Scenario: Other VPCs are ignored

- **WHEN** `vpc` names one of two VPCs in the region
- **THEN** only that VPC's blocks are reported

#### Scenario: The VPC is gone

- **WHEN** `vpc` names a VPC that does not exist
- **THEN** the command exits 2 with empty stdout

### Requirement: VPC evidence covers every associated block

With `resource=vpc`, the source SHALL report each IPv4 and IPv6 block associated
with each VPC, including secondary blocks, with the VPC ID as the resource. Blocks
whose association has failed or ended SHALL be excluded, and those in any other
state SHALL be included.

#### Scenario: A secondary block

- **WHEN** a VPC has `10.20.0.0/16` and the associated secondary `100.64.0.0/20`
- **THEN** both are reported with that VPC ID

#### Scenario: A block that ended

- **WHEN** a block's association state is `disassociated` or `failed`
- **THEN** it is not reported

### Requirement: Subnet evidence

With `resource=subnet`, the source SHALL report each subnet's IPv4 and IPv6 blocks
with the subnet ID as the resource, so that a pool may be planned inside a VPC.

#### Scenario: Allocating inside a VPC

- **WHEN** pool `10.20.0.0/16` is a whole VPC whose only subnet is `10.20.0.0/24`,
  and a `/24` is allocated in a space whose source has `resource=subnet`
- **THEN** it reserves `10.20.1.0/24`

### Requirement: Reads are complete and bounded

The source SHALL read every page of results. Each request SHALL be limited to 30
seconds and SHALL honor cancellation. It SHALL call only the operations that
describe VPCs and subnets.

#### Scenario: More than one page

- **WHEN** the response is paginated
- **THEN** blocks from every page are reported

#### Scenario: A failure on a later page

- **WHEN** the second page fails
- **THEN** the source fails and nothing from the first page is used

#### Scenario: Minimal permissions

- **WHEN** the credentials allow only `ec2:DescribeVpcs` and `ec2:DescribeSubnets`
- **THEN** every command that reads the source succeeds

### Requirement: Failures say what to fix

Missing credentials, denied access, throttling, and timeouts SHALL be
distinguishable in the error. The error SHALL contain no account ID, ARN, or
response body.

#### Scenario: Denied access

- **WHEN** the caller lacks `ec2:DescribeVpcs`
- **THEN** the command exits 2 and stderr names the missing permission

#### Scenario: No credentials

- **WHEN** no AWS credentials are available
- **THEN** the command exits 2 and says which source needs them
