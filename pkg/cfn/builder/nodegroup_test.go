package builder_test

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	api "github.com/weaveworks/eksctl/pkg/apis/eksctl.io/v1alpha5"
	"github.com/weaveworks/eksctl/pkg/cfn/builder"
	"github.com/weaveworks/eksctl/pkg/cfn/builder/fakes"
	"github.com/weaveworks/eksctl/pkg/cfn/outputs"
	"github.com/weaveworks/eksctl/pkg/eks/mocks"
	vpcfakes "github.com/weaveworks/eksctl/pkg/vpc/fakes"
	gfnt "github.com/weaveworks/goformation/v4/cloudformation/types"
)

var _ = Describe("Unmanaged NodeGroup Template Builder", func() {
	var (
		ngrs                 *builder.NodeGroupResourceSet
		cfg                  *api.ClusterConfig
		ng                   *api.NodeGroup
		supportsManagedNodes bool
		forceAddCNIPolicy    bool
		fakeVPCImporter      *vpcfakes.FakeImporter
		mockEC2              = &mocks.EC2API{}
		mockIAM              = &mocks.IAMAPI{}
	)

	BeforeEach(func() {
		supportsManagedNodes = false
		forceAddCNIPolicy = false
		fakeVPCImporter = new(vpcfakes.FakeImporter)
		cfg, ng = newClusterAndNodeGroup()
	})

	JustBeforeEach(func() {
		ngrs = builder.NewNodeGroupResourceSet(mockEC2, mockIAM, cfg, ng, supportsManagedNodes, forceAddCNIPolicy, fakeVPCImporter)
	})

	Describe("AddAllResources", func() {
		var (
			addErr     error
			ngTemplate *fakes.FakeTemplate
		)

		JustBeforeEach(func() {
			addErr = ngrs.AddAllResources()
			ngTemplate = &fakes.FakeTemplate{}
			templateBody, err := ngrs.RenderJSON()
			Expect(err).ShouldNot(HaveOccurred())
			Expect(json.Unmarshal(templateBody, ngTemplate)).To(Succeed())
		})

		It("should not error", func() {
			Expect(addErr).NotTo(HaveOccurred())
		})

		It("should add a template description", func() {
			Expect(ngTemplate.Description).To(Equal("EKS nodes (AMI family: , SSH access: false, private networking: false) [created and managed by eksctl]"))
		})

		It("should add partition mappings", func() {
			Expect(ngTemplate.Mappings["ServicePrincipalPartitionMap"]).ToNot(BeNil())
		})

		It("should add outputs", func() {
			Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupFeaturePrivateNetworking))
			Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupFeatureSharedSecurityGroup))
			Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupFeatureLocalSecurityGroup))
		})

		Context("if ng.MinSize is nil", func() {
			BeforeEach(func() {
				ng.MinSize = nil
				ng.DesiredCapacity = aws.Int(5)
			})

			It("the value is set based on the set desired capacity", func() {
				Expect(ng.MinSize).To(Equal(aws.Int(5)))
			})

			Context("if ng.DesiredCapacity is nil", func() {
				BeforeEach(func() {
					ng.DesiredCapacity = nil
				})

				It("both values are set to the default desired capacity", func() {
					Expect(ng.MinSize).To(Equal(aws.Int(api.DefaultNodeCount)))
				})
			})
		})

		Context("if ng.DesiredCapacity < ng.MinSize", func() {
			BeforeEach(func() {
				ng.DesiredCapacity = aws.Int(1)
				ng.MinSize = aws.Int(5)
			})

			It("fails", func() {
				Expect(addErr).To(MatchError("--nodes value (1) cannot be lower than --nodes-min value (5)"))
			})
		})

		Context("if ng.MaxSize is nil", func() {
			BeforeEach(func() {
				ng.MaxSize = nil
				ng.DesiredCapacity = aws.Int(5)
			})

			It("the value is set based on the set desired capacity", func() {
				Expect(ng.MaxSize).To(Equal(aws.Int(5)))
			})

			Context("if ng.DesiredCapacity is nil", func() {
				BeforeEach(func() {
					ng.DesiredCapacity = nil
					ng.MinSize = aws.Int(3)
				})

				It("the value is set to the MinSize value", func() {
					Expect(ng.MaxSize).To(Equal(aws.Int(3)))
				})
			})

			Context("ng.DesiredCapacity > ng.MinSize", func() {
				BeforeEach(func() {
					ng.DesiredCapacity = aws.Int(5)
					ng.MaxSize = aws.Int(1)
				})

				It("fails", func() {
					Expect(addErr).To(MatchError("--nodes value (5) cannot be greater than --nodes-max value (1)"))
				})
			})
		})

		Context("ng.MaxSize < ng.MinSize", func() {
			BeforeEach(func() {
				ng.DesiredCapacity = nil
				ng.MaxSize = aws.Int(1)
				ng.MinSize = aws.Int(5)
			})

			It("fails", func() {
				Expect(addErr).To(MatchError("--nodes-min value (5) cannot be greater than --nodes-max value (1)"))
			})
		})

		Context("iam.InstanceProfileARN is set", func() {
			BeforeEach(func() {
				ng.IAM.InstanceProfileARN = "foo"
			})

			It("adds the InstanceProfileARN output", func() {
				Expect(ngTemplate.Outputs).To(HaveLen(4))
				Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceProfileARN))
			})

			Context("iam.InstanceRoleARN is set", func() {
				BeforeEach(func() {
					ng.IAM.InstanceRoleARN = "foo"
				})

				It("adds the InstanceRoleARN output", func() {
					Expect(ngTemplate.Outputs).To(HaveLen(5))
					Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceRoleARN))
					Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceProfileARN))
				})
			})
		})

		Context("iam.InstanceRoleARN is set (ng.InstanceProfileARN is not)", func() {
			BeforeEach(func() {
				ng.IAM.InstanceRoleARN = "foo"
			})

			It("adds a new InstanceProfileARN resource", func() {
				Expect(ngTemplate.Resources).To(HaveKey("NodeInstanceProfile"))
				Expect(ngTemplate.Resources["NodeInstanceProfile"].Properties.Path).To(Equal("/"))
				Expect(ngTemplate.Resources["NodeInstanceProfile"].Properties.Roles).To(Equal([]interface{}{"foo"}))
			})

			It("adds the InstanceRoleARN and InstanceProfileARN outputs", func() {
				Expect(ngTemplate.Outputs).To(HaveLen(5))
				Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceRoleARN))
				Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceProfileARN))
			})
		})

		Context("neither iam.InstanceRoleARN or ng.InstanceProfileARN are set", func() {
			It("creates a new role", func() {
				Expect(ngTemplate.Resources).To(HaveKey("NodeInstanceRole"))
				Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.Path).To(Equal("/"))
				Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.AssumeRolePolicyDocument).ToNot(BeNil())
			})

			It("sets the correct outputs", func() {
				Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceRoleARN))
				Expect(ngTemplate.Outputs).To(HaveKey(outputs.NodeGroupInstanceProfileARN))
			})

			Context("ng.InstanceRoleName is set", func() {
				BeforeEach(func() {
					ng.IAM.InstanceRoleName = "you-know-i-won-an-oscar-for-this-role"
				})

				It("sets the name on the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.RoleName).To(Equal("you-know-i-won-an-oscar-for-this-role"))
				})
			})

			Context("ng.InstanceRolePermissionsBoundary is set", func() {
				BeforeEach(func() {
					ng.IAM.InstanceRolePermissionsBoundary = "shall-not-pass"
				})

				It("sets the PermissionsBoundary on the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.PermissionsBoundary).To(Equal("shall-not-pass"))
				})
			})

			// TODO move into IAM tests?
			Context("attach policy arns are set", func() {
				BeforeEach(func() {
					ng.IAM.AttachPolicyARNs = []string{"arn:aws:iam::1234567890:role/foo"}
				})

				It("adds the provided policy and the AmazonEC2ContainerRegistryReadOnly policy", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(2))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEC2ContainerRegistryReadOnly")))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement("arn:aws:iam::1234567890:role/foo"))
				})

				Context("a given attach policy arn is invalid", func() {
					BeforeEach(func() {
						ng.IAM.AttachPolicyARNs = []string{"foo"}
					})

					It("adds the provided policy and the AmazonEC2ContainerRegistryReadOnly policy", func() {
						Expect(addErr).To(MatchError("arn: invalid prefix"))
					})
				})
			})

			Context("no attach policy arns are set for unmanaged nodes", func() {
				BeforeEach(func() {
					ng.IAM.AttachPolicyARNs = []string{}
				})

				It("adds the default policies to the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(3))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEC2ContainerRegistryReadOnly")))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEKSWorkerNodePolicy")))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEKS_CNI_Policy")))
				})

				Context("forceAddCNIPolicy is true", func() {
					BeforeEach(func() {
						forceAddCNIPolicy = true
					})

					It("adds the AmazonEKS_CNI_Policy", func() {
						Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(3))
						Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEKS_CNI_Policy")))
					})
				})

				Context("ng.IAM.WithOIDC is true", func() {
					BeforeEach(func() {
						cfg.IAM.WithOIDC = aws.Bool(true)
					})

					It("does not add the AmazonEKS_CNI_Policy", func() {
						Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(2))
						Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).ToNot(ContainElement(makePolicyARNRef("AmazonEKS_CNI_Policy")))
					})
				})
			})

			Context("ssm is enabled", func() {
				BeforeEach(func() {
					ng.SSH = &api.NodeGroupSSH{
						Allow:     aws.Bool(true),
						EnableSSM: aws.Bool(true),
					}
				})

				It("adds the AmazonSSMManagedInstanceCore arn to the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(4))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonSSMManagedInstanceCore")))
				})
			})

			Context("ng.IAM.WithAddonPolicies.ImageBuilder is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.ImageBuilder = aws.Bool(true)
				})

				It("adds the AmazonSSMManagedInstanceCore arn to the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(3))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("AmazonEC2ContainerRegistryPowerUser")))
				})
			})

			Context("ng.IAM.WithAddonPolicies.CloudWatch is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.CloudWatch = aws.Bool(true)
				})

				It("adds the AmazonSSMManagedInstanceCore arn to the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(4))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("CloudWatchAgentServerPolicy")))
				})
			})

			Context("ng.IAM.WithAddonPolicies.CloudWatch is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.CloudWatch = aws.Bool(true)
				})

				It("adds the AmazonSSMManagedInstanceCore arn to the role", func() {
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(HaveLen(4))
					Expect(ngTemplate.Resources["NodeInstanceRole"].Properties.ManagedPolicyArns).To(ContainElement(makePolicyARNRef("CloudWatchAgentServerPolicy")))
				})
			})

			Context("ng.WithAddonPolicies.AutoScaler is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.AutoScaler = aws.Bool(true)
				})

				It("adds the PolicyAutoScaling policy to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyAutoScaling"))
					Expect(ngTemplate.Resources["PolicyAutoScaling"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyAutoScaling"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.CertManager is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.CertManager = aws.Bool(true)
				})

				It("adds PolicyCertManagerChangeSet, PolicyCertManagerHostedZones and PolicyCertManagerGetChange to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyCertManagerChangeSet"))
					Expect(ngTemplate.Resources).To(HaveKey("PolicyCertManagerHostedZones"))
					Expect(ngTemplate.Resources).To(HaveKey("PolicyCertManagerGetChange"))

					Expect(ngTemplate.Resources["PolicyCertManagerChangeSet"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyCertManagerChangeSet"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
					Expect(ngTemplate.Resources["PolicyCertManagerHostedZones"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyCertManagerHostedZones"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
					Expect(ngTemplate.Resources["PolicyCertManagerGetChange"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyCertManagerGetChange"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.ExternalDNS is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.ExternalDNS = aws.Bool(true)
				})

				It("adds PolicyExternalDNSChangeSet and PolicyExternalDNSHostedZones", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyExternalDNSChangeSet"))
					Expect(ngTemplate.Resources).To(HaveKey("PolicyExternalDNSHostedZones"))

					Expect(ngTemplate.Resources["PolicyExternalDNSHostedZones"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyExternalDNSHostedZones"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
					Expect(ngTemplate.Resources["PolicyExternalDNSChangeSet"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyExternalDNSChangeSet"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.AppMesh is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.AppMesh = aws.Bool(true)
				})

				It("adds PolicyAppMesh to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyAppMesh"))

					Expect(ngTemplate.Resources["PolicyAppMesh"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyAppMesh"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.AppMeshPreview is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.AppMeshPreview = aws.Bool(true)
				})

				It("adds PolicyAppMeshPreview to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyAppMeshPreview"))

					Expect(ngTemplate.Resources["PolicyAppMeshPreview"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyAppMeshPreview"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.EBS is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.EBS = aws.Bool(true)
				})

				It("adds PolicyEBS to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyEBS"))

					Expect(ngTemplate.Resources["PolicyEBS"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyEBS"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.FSX is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.FSX = aws.Bool(true)
				})

				It("adds PolicyFSX to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyFSX"))

					Expect(ngTemplate.Resources["PolicyFSX"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyFSX"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.EFS is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.EFS = aws.Bool(true)
				})

				It("adds PolicyEFS and PolicyEFSEC2 to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyEFS"))
					Expect(ngTemplate.Resources).To(HaveKey("PolicyEFSEC2"))

					Expect(ngTemplate.Resources["PolicyEFS"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyEFS"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
					Expect(ngTemplate.Resources["PolicyEFSEC2"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyEFSEC2"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.AWSLoadBalancerController is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.AWSLoadBalancerController = aws.Bool(true)
				})

				It("adds PolicyAWSLoadBalancerController to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyAWSLoadBalancerController"))

					Expect(ngTemplate.Resources["PolicyAWSLoadBalancerController"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyAWSLoadBalancerController"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})

			Context("ng.WithAddonPolicies.XRay is set", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.XRay = aws.Bool(true)
				})

				It("adds PolicyXRay to the role", func() {
					Expect(ngTemplate.Resources).To(HaveKey("PolicyXRay"))

					Expect(ngTemplate.Resources["PolicyXRay"].Properties.Roles).To(HaveLen(1))
					Expect(isRefTo(ngTemplate.Resources["PolicyXRay"].Properties.Roles[0], "NodeInstanceRole")).To(BeTrue())
				})
			})
			// TODO end
		})

		Context("ng.SecurityGroups.WithLocal is disabled", func() {
			BeforeEach(func() {
				ng.SecurityGroups.WithLocal = aws.Bool(false)
			})

			It("no sg resources are added", func() {
				Expect(ngTemplate.Resources).ToNot(HaveKey("SG"))
			})
		})

		Context("adding security group resources", func() {
			var (
				vpcID = "some-vpc"
				sgID  = "some-sg"
			)
			BeforeEach(func() {
				fakeVPCImporter.VPCReturns(gfnt.MakeFnImportValueString(vpcID))
				fakeVPCImporter.ControlPlaneSecurityGroupReturns(gfnt.MakeFnImportValueString(sgID))
			})

			It("the SG resource is added", func() {
				Expect(ngTemplate.Resources).To(HaveKey("SG"))
				properties := ngTemplate.Resources["SG"].Properties
				Expect(properties.VpcID).To(ContainElement(vpcID))
				Expect(properties.GroupDescription).To(Equal("Communication between the control plane and worker nodes in group ng-abcd1234"))
				Expect(properties.Tags[0].Key).To(Equal("kubernetes.io/cluster/bonsai"))
				Expect(properties.Tags[0].Value).To(Equal("owned"))
				Expect(properties.SecurityGroupIngress).To(HaveLen(2))
				Expect(properties.SecurityGroupIngress[0].SourceSecurityGroupID).To(ContainElement(sgID))
				Expect(properties.SecurityGroupIngress[0].Description).To(Equal("[IngressInterCluster] Allow worker nodes in group ng-abcd1234 to communicate with control plane (kubelet and workload TCP ports)"))
				Expect(properties.SecurityGroupIngress[0].IPProtocol).To(Equal("tcp"))
				Expect(properties.SecurityGroupIngress[0].FromPort).To(Equal(float64(1025)))
				Expect(properties.SecurityGroupIngress[0].ToPort).To(Equal(float64(65535)))
				Expect(properties.SecurityGroupIngress[1].SourceSecurityGroupID).To(ContainElement(sgID))
				Expect(properties.SecurityGroupIngress[1].Description).To(Equal("[IngressInterClusterAPI] Allow worker nodes in group ng-abcd1234 to communicate with control plane (workloads using HTTPS port, commonly used with extension API servers)"))
				Expect(properties.SecurityGroupIngress[1].IPProtocol).To(Equal("tcp"))
				Expect(properties.SecurityGroupIngress[1].FromPort).To(Equal(float64(443)))
				Expect(properties.SecurityGroupIngress[1].ToPort).To(Equal(float64(443)))
			})

			It("the EgressInterCluster resource is added", func() {
				Expect(ngTemplate.Resources).To(HaveKey("EgressInterCluster"))
				properties := ngTemplate.Resources["EgressInterCluster"].Properties
				Expect(properties.GroupID).To(ContainElement(sgID))
				Expect(properties.DestinationSecurityGroupID).To(Equal(makeRef("SG")))
				Expect(properties.Description).To(Equal("Allow control plane to communicate with worker nodes in group ng-abcd1234 (kubelet and workload TCP ports)"))
				Expect(properties.IPProtocol).To(Equal("tcp"))
				Expect(properties.FromPort).To(Equal(1025))
				Expect(properties.ToPort).To(Equal(65535))
			})

			It("the EgressInterClusterAPI resource is added", func() {
				Expect(ngTemplate.Resources).To(HaveKey("EgressInterClusterAPI"))
				properties := ngTemplate.Resources["EgressInterClusterAPI"].Properties
				Expect(properties.GroupID).To(ContainElement(sgID))
				Expect(properties.DestinationSecurityGroupID).To(Equal(makeRef("SG")))
				Expect(properties.Description).To(Equal("Allow control plane to communicate with worker nodes in group ng-abcd1234 (workloads using HTTPS port, commonly used with extension API servers)"))
				Expect(properties.IPProtocol).To(Equal("tcp"))
				Expect(properties.FromPort).To(Equal(443))
				Expect(properties.ToPort).To(Equal(443))
			})

			It("the IngressInterClusterCP resource is added", func() {
				Expect(ngTemplate.Resources).To(HaveKey("IngressInterClusterCP"))
				properties := ngTemplate.Resources["IngressInterClusterCP"].Properties
				Expect(properties.GroupID).To(ContainElement(sgID))
				Expect(properties.SourceSecurityGroupID).To(Equal(makeRef("SG")))
				Expect(properties.Description).To(Equal("Allow control plane to receive API requests from worker nodes in group ng-abcd1234"))
				Expect(properties.IPProtocol).To(Equal("tcp"))
				Expect(properties.FromPort).To(Equal(443))
				Expect(properties.ToPort).To(Equal(443))
			})

			Context("ng.EFA is enabled", func() {
				BeforeEach(func() {
					ng.EFAEnabled = aws.Bool(true)
					mockEC2.On("DescribeInstanceTypes",
						&ec2.DescribeInstanceTypesInput{
							InstanceTypes: aws.StringSlice([]string{"m5.large"}),
						},
					).Return(
						&ec2.DescribeInstanceTypesOutput{
							InstanceTypes: []*ec2.InstanceTypeInfo{
								{
									InstanceType: aws.String("m5.large"),
									NetworkInfo: &ec2.NetworkInfo{
										EfaSupported:        aws.Bool(true),
										MaximumNetworkCards: aws.Int64(4),
									},
								},
							},
						}, nil,
					)
				})

				It("adds the efa sg resources", func() {
					Expect(ngTemplate.Resources).To(HaveKey("EFASG"))
					properties := ngTemplate.Resources["EFASG"].Properties
					Expect(properties.VpcID).To(ContainElement(vpcID))
					Expect(properties.GroupDescription).To(Equal("EFA-enabled security group"))
					Expect(properties.Tags[0].Key).To(Equal("kubernetes.io/cluster/bonsai"))
					Expect(properties.Tags[0].Value).To(Equal("owned"))

					Expect(ngTemplate.Resources).To(HaveKey("EFAEgressSelf"))
					properties = ngTemplate.Resources["EFAEgressSelf"].Properties
					Expect(properties.GroupID).To(Equal(makeRef("EFASG")))
					Expect(properties.IPProtocol).To(Equal("-1"))
					Expect(properties.Description).To(Equal("Allow worker nodes in group ng-abcd1234 to communicate to itself (EFA-enabled)"))

					Expect(ngTemplate.Resources).To(HaveKey("EFAIngressSelf"))
					properties = ngTemplate.Resources["EFAIngressSelf"].Properties
					Expect(properties.GroupID).To(Equal(makeRef("EFASG")))
					Expect(properties.IPProtocol).To(Equal("-1"))
					Expect(properties.Description).To(Equal("Allow worker nodes in group ng-abcd1234 to communicate to itself (EFA-enabled)"))
				})
			})
		})

		Context("adding resources for nodegroup", func() {
			BeforeEach(func() {
				ng.AMI = "ami-123"
			})

			It("creates new NodeGroupLaunchTemplate resource", func() {
				Expect(ngTemplate.Resources).To(HaveKey("NodeGroupLaunchTemplate"))
				properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
				Expect(properties.LaunchTemplateName).To(Equal(map[string]interface{}{"Fn::Sub": "${AWS::StackName}"}))
				Expect(properties.LaunchTemplateData.IamInstanceProfile.Arn).To(Equal(makeIamInstanceProfileRef()))
				Expect(properties.LaunchTemplateData.ImageID).To(Equal("ami-123"))
				Expect(properties.LaunchTemplateData.UserData).To(Equal(""))
				Expect(properties.LaunchTemplateData.InstanceType).To(Equal("m5.large"))
				Expect(properties.LaunchTemplateData.MetadataOptions.HTTPPutResponseHopLimit).To(Equal(float64(2)))
				Expect(properties.LaunchTemplateData.MetadataOptions.HTTPTokens).To(Equal("optional"))
			})

			Context("ng.DisableIMDSv1 is enabled", func() {
				BeforeEach(func() {
					ng.DisableIMDSv1 = aws.Bool(true)
				})

				It("sets HttpTokens to required on the LaunchTemplateData MetadataOptions", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.MetadataOptions.HTTPTokens).To(Equal("required"))
				})
			})

			Context("ng.DisablePodIMDS is enabled", func() {
				BeforeEach(func() {
					ng.DisablePodIMDS = aws.Bool(true)
				})

				It("sets HttpTokens to required on the LaunchTemplateData MetadataOptions and sets hopLimit to 1", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.MetadataOptions.HTTPTokens).To(Equal("required"))
					Expect(properties.LaunchTemplateData.MetadataOptions.HTTPPutResponseHopLimit).To(Equal(float64(1)))
				})
			})

			Context("ng.EFAEnabled is true and ng.Placement is nil", func() {
				BeforeEach(func() {
					ng.EFAEnabled = aws.Bool(true)
				})

				It("creates NodeGroupPlacementGroup resource", func() {
					Expect(ngTemplate.Resources).To(HaveKey("NodeGroupPlacementGroup"))
					Expect(ngTemplate.Resources["NodeGroupPlacementGroup"].Properties.Strategy).To(Equal("cluster"))
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.Placement.GroupName).To(Equal(makeRef("NodeGroupPlacementGroup")))
				})
			})

			Context("mixed instances are set", func() {
				BeforeEach(func() {
					ng.InstancesDistribution = &api.NodeGroupInstancesDistribution{
						InstanceTypes: []string{"type-1", "type-2"},
					}
				})

				It("sets the ng instance type to the first in the instances distribution list", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.InstanceType).To(Equal("type-1"))
				})
			})

			Context("ng.EBSOptimized is true", func() {
				BeforeEach(func() {
					ng.EBSOptimized = aws.Bool(true)
				})

				It("enables the value on the launch template", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.EbsOptimized).To(Equal(aws.Bool(true)))
				})
			})

			Context("ng.CPUCredits are set", func() {
				BeforeEach(func() {
					ng.CPUCredits = aws.String("major-street-cred")
				})

				It("enables the value on the launch template", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.CreditSpecification.CPUCredits).To(Equal("major-street-cred"))
				})
			})

			Context("ng.Placement is set", func() {
				BeforeEach(func() {
					ng.Placement = &api.Placement{GroupName: "one-direction"}
				})

				It("sets the value on the LaunchTemplateData", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.Placement.GroupName).To(Equal("one-direction"))
				})
			})

			It("creates new NodeGroup resource", func() {
				Expect(ngTemplate.Resources).To(HaveKey("NodeGroup"))
				Expect(ngTemplate.Resources["NodeGroup"].Type).To(Equal("AWS::AutoScaling::AutoScalingGroup"))
				Expect(ngTemplate.Resources["NodeGroup"].UpdatePolicy["AutoScalingRollingUpdate"]).To(Equal(map[string]interface{}{}))
				Expect(ngTemplate.Resources["NodeGroup"].Properties.LaunchTemplate.LaunchTemplateName).To(Equal(map[string]interface{}{"Fn::Sub": "${AWS::StackName}"}))
				Expect(ngTemplate.Resources["NodeGroup"].Properties.LaunchTemplate.Version["Fn::GetAtt"]).To(Equal([]interface{}{"NodeGroupLaunchTemplate", "LatestVersionNumber"}))
				tags := ngTemplate.Resources["NodeGroup"].Properties.Tags
				Expect(tags).To(HaveLen(2))
				Expect(tags[0].Key).To(Equal("Name"))
				Expect(tags[0].Value).To(Equal("bonsai-ng-abcd1234-Node"))
				Expect(tags[0].PropagateAtLaunch).To(Equal("true"))
				Expect(tags[1].Key).To(Equal("kubernetes.io/cluster/bonsai"))
				Expect(tags[1].Value).To(Equal("owned"))
				Expect(tags[1].PropagateAtLaunch).To(Equal("true"))
			})

			Context("ng.InstanceName is set", func() {
				BeforeEach(func() {
					ng.InstanceName = "great-name"
				})

				It("tags the resource with the given name", func() {
					tags := ngTemplate.Resources["NodeGroup"].Properties.Tags
					Expect(tags[0].Key).To(Equal("Name"))
					Expect(tags[0].Value).To(Equal("great-name"))
				})
			})

			Context("ng.InstancePrefix is set", func() {
				BeforeEach(func() {
					ng.InstancePrefix = "cute"
				})

				It("prepends the resource name tag", func() {
					tags := ngTemplate.Resources["NodeGroup"].Properties.Tags
					Expect(tags[0].Key).To(Equal("Name"))
					Expect(tags[0].Value).To(Equal("cute-bonsai-ng-abcd1234-Node"))
				})
			})

			Context("ng.InstancesDistribution and ng.InstancesDistribution.CapacityRebalance are set", func() {
				BeforeEach(func() {
					ng.InstancesDistribution = &api.NodeGroupInstancesDistribution{
						CapacityRebalance: true,
					}
				})

				It("sets CapacityRebalance on the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.CapacityRebalance).To(BeTrue())
				})
			})

			Context("ng.DesiredCapacity is set", func() {
				BeforeEach(func() {
					ng.DesiredCapacity = aws.Int(5)
				})

				It("sets DesiredCapacity on the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.DesiredCapacity).To(Equal("5"))
				})
			})

			Context("ng.MinSize is set", func() {
				BeforeEach(func() {
					ng.MinSize = aws.Int(3)
				})

				It("sets MinSize on the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.MinSize).To(Equal("3"))
				})
			})

			Context("ng.MaxSize is set", func() {
				BeforeEach(func() {
					ng.MaxSize = aws.Int(7)
				})

				It("sets MaxSize on the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.MaxSize).To(Equal("7"))
				})
			})

			Context("ng.ASGMetricsCollection is set", func() {
				BeforeEach(func() {
					ng.ASGMetricsCollection = []api.MetricsCollection{{Granularity: "idk"}}
				})

				It("sets metrics collection on the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.MetricsCollection).To(HaveLen(1))
					Expect(ngTemplate.Resources["NodeGroup"].Properties.MetricsCollection[0]["Granularity"]).To(Equal("idk"))
				})

				Context("ng.ASGMetricsCollection.Metrics are set", func() {
					BeforeEach(func() {
						ng.ASGMetricsCollection = []api.MetricsCollection{{
							Granularity: "idk",
							Metrics:     []string{"wut"},
						}}
					})

					It("adds these to the metrics collection", func() {
						Expect(ngTemplate.Resources["NodeGroup"].Properties.MetricsCollection[0]["Granularity"]).To(Equal("idk"))
						Expect(ngTemplate.Resources["NodeGroup"].Properties.MetricsCollection[0]["Metrics"]).To(Equal([]interface{}{"wut"}))
					})
				})
			})

			Context("ng.ClassicLoadBalancerNames are set", func() {
				BeforeEach(func() {
					ng.ClassicLoadBalancerNames = []string{"what-a-classic"}
				})

				It("adds the LB name to the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.LoadBalancerNames).To(HaveLen(1))
					Expect(ngTemplate.Resources["NodeGroup"].Properties.LoadBalancerNames[0]).To(Equal("what-a-classic"))
				})
			})

			Context("ng.TargetGroupARNs are set", func() {
				BeforeEach(func() {
					ng.TargetGroupARNs = []string{"target-acquired"}
				})

				It("adds the LB name to the resource", func() {
					Expect(ngTemplate.Resources["NodeGroup"].Properties.TargetGroupARNs).To(HaveLen(1))
					Expect(ngTemplate.Resources["NodeGroup"].Properties.TargetGroupARNs[0]).To(Equal("target-acquired"))
				})
			})

			Context("has mixed instances", func() {
				BeforeEach(func() {
					ng.InstancesDistribution = &api.NodeGroupInstancesDistribution{
						InstanceTypes: []string{"type-1", "type-2"},
					}
				})

				It("adds the mixed instance policy to the resource", func() {
					policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy.LaunchTemplate
					Expect(policyTemplate.LaunchTemplateSpecification.LaunchTemplateName["Fn::Sub"]).To(Equal("${AWS::StackName}"))
					Expect(policyTemplate.LaunchTemplateSpecification.Version["Fn::GetAtt"]).To(Equal([]interface{}{"NodeGroupLaunchTemplate", "LatestVersionNumber"}))
					Expect(policyTemplate.Overrides[0].InstanceType).To(Equal("type-1"))
					Expect(policyTemplate.Overrides[1].InstanceType).To(Equal("type-2"))
				})

				Context("ng.InstancesDistribution.MaxPrice is not nil", func() {
					BeforeEach(func() {
						ng.InstancesDistribution.MaxPrice = aws.Float64(20)
					})

					It("adds max price to the mixed instance policy", func() {
						policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy
						Expect(policyTemplate.InstancesDistribution.SpotMaxPrice).To(Equal("20.000000"))
					})
				})

				Context("ng.InstancesDistribution.OnDemandBaseCapacity is not nil", func() {
					BeforeEach(func() {
						ng.InstancesDistribution.OnDemandBaseCapacity = aws.Int(2)
					})

					It("adds on demand base capacity to the mixed instance policy", func() {
						policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy
						Expect(policyTemplate.InstancesDistribution.OnDemandBaseCapacity).To(Equal("2"))
					})
				})

				Context("ng.InstancesDistribution.OnDemandPercentageAboveBaseCapacity is not nil", func() {
					BeforeEach(func() {
						ng.InstancesDistribution.OnDemandPercentageAboveBaseCapacity = aws.Int(2)
					})

					It("adds on demand percentage above capacity to the mixed instance policy", func() {
						policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy
						Expect(policyTemplate.InstancesDistribution.OnDemandPercentageAboveBaseCapacity).To(Equal("2"))
					})
				})

				Context("ng.InstancesDistribution.SpotInstancePools is not nil", func() {
					BeforeEach(func() {
						ng.InstancesDistribution.SpotInstancePools = aws.Int(2)
					})

					It("adds spot instance pools to the mixed instance policy", func() {
						policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy
						Expect(policyTemplate.InstancesDistribution.SpotInstancePools).To(Equal("2"))
					})
				})

				Context("ng.InstancesDistribution.SpotAllocationStrategy is not nil", func() {
					BeforeEach(func() {
						ng.InstancesDistribution.SpotAllocationStrategy = aws.String("foo")
					})

					It("adds spot instance pools to the mixed instance policy", func() {
						policyTemplate := ngTemplate.Resources["NodeGroup"].Properties.MixedInstancesPolicy
						Expect(policyTemplate.InstancesDistribution.SpotAllocationStrategy).To(Equal("foo"))
					})
				})
			})

			Context("ng.ASGSuspendProcesses are set", func() {
				BeforeEach(func() {
					ng.ASGSuspendProcesses = []string{"stuff"}
				})

				It("sets SuspendProcesses on the update policy", func() {
					Expect(ngTemplate.Resources["NodeGroup"].UpdatePolicy["AutoScalingRollingUpdate"]["SuspendProcesses"]).To(Equal([]interface{}{"stuff"}))
				})
			})

			Context("ng.IAM.WithAddonPolicies.AutoScaler is enabled", func() {
				BeforeEach(func() {
					ng.IAM.WithAddonPolicies.AutoScaler = aws.Bool(true)
				})

				It("appends autoscaling tags to the ASG", func() {
					tags := ngTemplate.Resources["NodeGroup"].Properties.Tags
					Expect(tags).To(HaveLen(4))
					Expect(tags[2].Key).To(Equal("k8s.io/cluster-autoscaler/enabled"))
					Expect(tags[2].Value).To(Equal("true"))
					Expect(tags[2].PropagateAtLaunch).To(Equal("true"))
					Expect(tags[3].Key).To(Equal("k8s.io/cluster-autoscaler/bonsai"))
					Expect(tags[3].Value).To(Equal("owned"))
					Expect(tags[3].PropagateAtLaunch).To(Equal("true"))
				})
			})

			Context("ng.SSH.PublicKeyName", func() {
				BeforeEach(func() {
					ng.SSH = &api.NodeGroupSSH{
						Allow:         aws.Bool(true),
						PublicKeyName: aws.String("a-key"),
					}
				})

				It("the key is added to the launch template data", func() {
					properties := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties
					Expect(properties.LaunchTemplateData.KeyName).To(Equal("a-key"))
				})
			})

			Context("ng.VolumeSize > 0", func() {
				BeforeEach(func() {
					ng.VolumeSize = aws.Int(20)
				})

				It("block device mappings are set on the launch template", func() {
					Expect(ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings).To(HaveLen(1))
					mapping := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings[0]
					Expect(mapping.DeviceName).To(Equal("/dev/xvda"))
					Expect(mapping.Ebs["Encrypted"]).To(Equal(false))
					Expect(mapping.Ebs["VolumeSize"]).To(Equal(float64(20)))
					Expect(mapping.Ebs["VolumeType"]).To(Equal("gp2"))
				})

				Context("ng.VolumeKmsKeyID is set", func() {
					BeforeEach(func() {
						ng.VolumeKmsKeyID = aws.String("key-id")
					})

					It("the kms key id is set on the block device mapping", func() {
						mapping := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings[0]
						Expect(mapping.Ebs["KmsKeyId"]).To(Equal("key-id"))
					})
				})

				Context("ng.VolumeType is IO1", func() {
					BeforeEach(func() {
						ng.VolumeType = aws.String(api.NodeVolumeTypeIO1)
						ng.VolumeIOPS = aws.Int(500)
					})

					It("IOPS are set on the block device mapping", func() {
						mapping := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings[0]
						Expect(mapping.Ebs["Iops"]).To(Equal(float64(500)))
					})
				})

				Context("ng.VolumeType is GP3", func() {
					BeforeEach(func() {
						ng.VolumeType = aws.String(api.NodeVolumeTypeGP3)
						ng.VolumeIOPS = aws.Int(500)
						ng.VolumeThroughput = aws.Int(500)
					})

					It("IOPS and Throughput are set on the block device mapping", func() {
						mapping := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings[0]
						Expect(mapping.Ebs["Iops"]).To(Equal(float64(500)))
						Expect(mapping.Ebs["Throughput"]).To(Equal(float64(500)))
					})
				})

				Context("ng.AdditionalEncryptedVolume is set", func() {
					BeforeEach(func() {
						ng.AdditionalEncryptedVolume = "/foo/bar"
					})

					It("the volume is added to the launch template block device mappings", func() {
						Expect(ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings).To(HaveLen(2))
						mapping := ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.BlockDeviceMappings[1]
						Expect(mapping.DeviceName).To(Equal("/foo/bar"))
						Expect(mapping.Ebs["Encrypted"]).To(Equal(false))
					})
				})
			})

			Context("ng.SecurityGroups.AttachIDs are set", func() {
				BeforeEach(func() {
					ng.SecurityGroups.AttachIDs = []string{"foo"}
				})

				It("those sgs are added to the launchTemplate", func() {
					Expect(ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.NetworkInterfaces).To(HaveLen(1))
				})
			})

			Context("ng.SecurityGroups.WithShared is set", func() {
				BeforeEach(func() {
					ng.SecurityGroups.WithShared = aws.Bool(true)
				})

				It("that sg is added to the launchTemplate", func() {
					Expect(ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.NetworkInterfaces).To(HaveLen(1))
				})
			})

			Context("ng.EFAEnabled is set", func() {
				BeforeEach(func() {
					ng.EFAEnabled = aws.Bool(true)
				})

				It("the EFA sgs are added to the launchTemplate", func() {
					Expect(ngTemplate.Resources["NodeGroupLaunchTemplate"].Properties.LaunchTemplateData.NetworkInterfaces).To(HaveLen(4))
				})
			})
		})
	})

	Describe("AssignSubnets", func() {
		var ngBase *api.NodeGroupBase
		BeforeEach(func() {
			ngBase = ng.NodeGroupBase
			fakeVPCImporter.SubnetsPublicReturns(gfnt.NewString("subnet-1"))
		})

		It("returns public subnets", func() {
			subnets, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(subnets).To(Equal(gfnt.NewString("subnet-1")))
		})

		Context("when private networking is enabled", func() {
			BeforeEach(func() {
				fakeVPCImporter.SubnetsPrivateReturns(gfnt.NewString("subnet-2"))
				ngBase.PrivateNetworking = true
			})

			It("returns private subnets", func() {
				subnets, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnets).To(Equal(gfnt.NewString("subnet-2")))
			})
		})

		Context("when AvailabilityZones are set", func() {
			BeforeEach(func() {
				ngBase.Subnets = []string{publicSubnet1, publicSubnet2}
				cfg.AvailabilityZones = []string{"us-west-2a", "us-west-2b"}
			})

			It("maps subnets to azs", func() {
				subnets, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnets).To(Equal(gfnt.NewStringSlice(publicSubnet1, publicSubnet2)))
			})

			Context("private networking is enabled", func() {
				BeforeEach(func() {
					ngBase.Subnets = []string{privateSubnet1, privateSubnet2}
					ngBase.PrivateNetworking = true
				})

				It("maps private subnets to azs", func() {
					subnets, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
					Expect(err).NotTo(HaveOccurred())
					Expect(subnets).To(Equal(gfnt.NewStringSlice(privateSubnet1, privateSubnet2)))
				})
			})

			Context("selecting subnets per az fails", func() {
				BeforeEach(func() {
					ngBase.Subnets = []string{"not-a-thing"}
				})

				It("returns the error", func() {
					_, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
					Expect(err).To(MatchError(ContainSubstring("couldn't find public subnets")))
				})
			})
		})

		Context("when EFA is enabled and > 1 subnets are set", func() {
			BeforeEach(func() {
				ngBase.Subnets = []string{publicSubnet1, publicSubnet2}
				ngBase.EFAEnabled = aws.Bool(true)
			})

			It("choses only the first subnet", func() {
				subnets, err := builder.AssignSubnets(ngBase, fakeVPCImporter, cfg)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnets).To(Equal(gfnt.NewStringSlice(publicSubnet1)))
			})
		})
	})
})

func newClusterAndNodeGroup() (*api.ClusterConfig, *api.NodeGroup) {
	cfg := api.NewClusterConfig()
	cfg.Metadata.Name = "bonsai"
	ng := cfg.NewNodeGroup()
	ng.Name = "ng-abcd1234"
	ng.VolumeType = new(string)
	*ng.VolumeType = api.NodeVolumeTypeGP2
	ng.VolumeName = new(string)
	*ng.VolumeName = "/dev/xvda"
	ng.VolumeEncrypted = api.Disabled()
	cfg.VPC = vpcConfig()
	return cfg, ng
}

func makeIamInstanceProfileRef() map[string]interface{} {
	return map[string]interface{}{
		"Fn::GetAtt": []interface{}{"NodeInstanceProfile", "Arn"},
	}
}
