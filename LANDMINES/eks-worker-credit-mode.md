# EKS worker credits can differ from Terraform state

## Applies when

Creating or replacing the AWS demo's burstable EKS workers from `infra/aws`.

## Symptom

Terraform records `credit_specification.cpu_credits = "standard"`, but EC2 reports Unlimited for the running workers. A live deployment reproduced this on both workers. Reads of the requested and effective launch-template versions omitted `CreditSpecification`; the underlying cause is not yet established.

Unlimited mode can incur surplus CPU-credit charges excluded by the Standard-mode cost assumption. Terraform's template state is not proof of the instances' actual mode.

## Proceed

Resolve the managed node group's actual Auto Scaling instance IDs. Read their mode with `aws ec2 describe-instance-credit-specifications` after initial creation and every replacement. Before applying the Standard-mode estimate to those workers, require `CpuCredits: standard` for each instance.

The current mitigation is a per-instance `aws ec2 modify-instance-credit-specification` call selecting only verified demo workers, followed by a read-back. This does not persist across replacement. Avoid changing the account-wide T3 default, which affects other workloads.

The durable fix is tracked in [issue #295](https://github.com/AY2627S1-CS3219-P1/FoC/issues/295). Any additional resources or worker replacement require a separately reviewed plan.

## Removal condition

Verify that both initial creation and an authorized replacement produce Standard-mode workers without a per-instance correction, then close the issue and remove this entry.
