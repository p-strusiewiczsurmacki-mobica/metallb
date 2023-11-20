package conversion

import (
	"fmt"
	"sort"
	"time"

	"go.universe.tf/metallb/api/v1beta1"
	"go.universe.tf/metallb/api/v1beta2"
	"go.universe.tf/metallb/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func addResources(first, second *config.ClusterResources) config.ClusterResources {
	if first == nil {
		first = &config.ClusterResources{}
	}
	first.Pools = append(first.Pools, second.Pools...)
	first.Peers = append(first.Peers, second.Peers...)
	first.BFDProfiles = append(first.BFDProfiles, second.BFDProfiles...)
	first.BGPAdvs = append(first.BGPAdvs, second.BGPAdvs...)
	first.L2Advs = append(first.L2Advs, second.L2Advs...)
	first.LegacyAddressPools = append(first.LegacyAddressPools, second.LegacyAddressPools...)
	first.Communities = append(first.Communities, second.Communities...)

	if first.PasswordSecrets != nil && second.PasswordSecrets != nil {
		for key, value := range second.PasswordSecrets {
			first.PasswordSecrets[key] = value
		}
	}

	first.Nodes = append(first.Nodes, second.Nodes...)
	first.Namespaces = append(first.Namespaces, second.Namespaces...)

	return *first
}

func bfdProfileFor(c *configFile) []v1beta1.BFDProfile {
	ret := make([]v1beta1.BFDProfile, len(c.BFDProfiles))

	for i, bfd := range c.BFDProfiles {
		b := v1beta1.BFDProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bfd.Name,
				Namespace: resourcesNameSpace,
			},
			Spec: v1beta1.BFDProfileSpec{
				ReceiveInterval:  bfd.ReceiveInterval,
				TransmitInterval: bfd.TransmitInterval,
				DetectMultiplier: bfd.DetectMultiplier,
				EchoInterval:     bfd.EchoInterval,
				EchoMode:         &c.BFDProfiles[i].EchoMode,
				PassiveMode:      &c.BFDProfiles[i].PassiveMode,
				MinimumTTL:       bfd.MinimumTTL,
			},
		}
		ret[i] = b
	}
	return ret
}

// communitiesFor aggregates all the community aliases into one community resource.
func communitiesFor(cf *configFile) []v1beta1.Community {
	if len(cf.BGPCommunities) == 0 {
		return nil
	}

	communitiesAliases := make([]v1beta1.CommunityAlias, 0)
	// in order to make the rendering stable, we must have a sorted list of communities.
	sortedCommunities := make([]string, 0, len(cf.BGPCommunities))

	for n := range cf.BGPCommunities {
		sortedCommunities = append(sortedCommunities, n)
	}
	sort.Strings(sortedCommunities)

	for _, v := range sortedCommunities {
		communityAlias := v1beta1.CommunityAlias{
			Name:  v,
			Value: cf.BGPCommunities[v],
		}
		communitiesAliases = append(communitiesAliases, communityAlias)
	}

	res := v1beta1.Community{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "communities",
			Namespace: resourcesNameSpace,
		},
		Spec: v1beta1.CommunitySpec{
			Communities: communitiesAliases,
		},
	}
	return []v1beta1.Community{res}
}

func peersFor(c *configFile) ([]v1beta2.BGPPeer, error) {
	res := make([]v1beta2.BGPPeer, 0)
	for i, peer := range c.Peers {
		p, err := parsePeer(peer)
		if err != nil {
			return nil, err
		}
		p.Name = fmt.Sprintf("peer%d", i+1)
		p.Namespace = resourcesNameSpace
		res = append(res, *p)
	}
	return res, nil
}

func parsePeer(p peer) (*v1beta2.BGPPeer, error) {
	holdTime, err := parseHoldTime(p.HoldTime)
	if err != nil {
		return nil, err
	}

	nodeSels := make([]metav1.LabelSelector, 0)
	for _, sel := range p.NodeSelectors {
		s := parseNodeSelector(sel)
		nodeSels = append(nodeSels, s)
	}

	res := &v1beta2.BGPPeer{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: resourcesNameSpace,
		},
		Spec: v1beta2.BGPPeerSpec{
			MyASN:         p.MyASN,
			ASN:           p.ASN,
			Address:       p.Addr,
			SrcAddress:    p.SrcAddr,
			Port:          p.Port,
			HoldTime:      metav1.Duration{Duration: holdTime},
			RouterID:      p.RouterID,
			NodeSelectors: nodeSels,
			Password:      p.Password,
			BFDProfile:    p.BFDProfile,
			EBGPMultiHop:  p.EBGPMultiHop,
		},
	}
	if p.KeepaliveTime != "" {
		keepaliveTime, err := parseKeepaliveTime(p.KeepaliveTime)
		if err != nil {
			return nil, err
		}
		res.Spec.KeepaliveTime = metav1.Duration{Duration: keepaliveTime}
	}

	return res, nil
}

func ipAddressPoolsFor(c *configFile) []v1beta1.IPAddressPool {
	res := make([]v1beta1.IPAddressPool, len(c.Pools))
	for i, addresspool := range c.Pools {
		var ap v1beta1.IPAddressPool
		ap.Name = addresspool.Name
		ap.Namespace = resourcesNameSpace
		ap.Spec.Addresses = make([]string, len(addresspool.Addresses))
		copy(ap.Spec.Addresses, addresspool.Addresses)
		if addresspool.AvoidBuggyIPs != nil {
			ap.Spec.AvoidBuggyIPs = *addresspool.AvoidBuggyIPs
		}
		ap.Spec.AutoAssign = addresspool.AutoAssign
		res[i] = ap
	}
	return res
}

func bgpAdvertisementsFor(c *configFile) []v1beta1.BGPAdvertisement {
	res := make([]v1beta1.BGPAdvertisement, 0)
	index := 1
	for _, ap := range c.Pools {
		for _, bgpAdv := range ap.BGPAdvertisements {
			var b v1beta1.BGPAdvertisement
			b.Name = fmt.Sprintf("bgpadvertisement%d", index)
			index++
			b.Namespace = resourcesNameSpace
			b.Spec.Communities = make([]string, len(bgpAdv.Communities))
			copy(b.Spec.Communities, bgpAdv.Communities)
			b.Spec.AggregationLength = bgpAdv.AggregationLength
			b.Spec.AggregationLengthV6 = bgpAdv.AggregationLengthV6
			b.Spec.LocalPref = bgpAdv.LocalPref
			b.Spec.IPAddressPools = []string{ap.Name}
			res = append(res, b)
		}
		if len(ap.BGPAdvertisements) == 0 && ap.Protocol == BGP {
			res = append(res, emptyBGPAdv(ap.Name, index))
			index++
		}
	}
	return res
}

func emptyBGPAdv(addressPoolName string, index int) v1beta1.BGPAdvertisement {
	return v1beta1.BGPAdvertisement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("bgpadvertisement%d", index),
			Namespace: resourcesNameSpace,
		},
		Spec: v1beta1.BGPAdvertisementSpec{
			IPAddressPools: []string{addressPoolName},
		},
	}
}

func l2AdvertisementsFor(c *configFile) []v1beta1.L2Advertisement {
	res := make([]v1beta1.L2Advertisement, 0)
	index := 1
	for _, addresspool := range c.Pools {
		if addresspool.Protocol == Layer2 {
			l2Adv := v1beta1.L2Advertisement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("l2advertisement%d", index),
					Namespace: resourcesNameSpace,
				},
				Spec: v1beta1.L2AdvertisementSpec{
					IPAddressPools: []string{addresspool.Name},
				},
			}
			index++
			res = append(res, l2Adv)
		}
	}
	return res
}

func parseKeepaliveTime(ka string) (time.Duration, error) {
	d, err := time.ParseDuration(ka)
	if err != nil {
		return 0, fmt.Errorf("invalid keepalive time %q: %s", ka, err)
	}
	rounded := time.Duration(int(d.Seconds())) * time.Second
	return rounded, nil
}

func parseNodeSelector(sel nodeSelector) metav1.LabelSelector {
	res := metav1.LabelSelector{}

	res.MatchLabels = sel.MatchLabels
	res.MatchExpressions = []metav1.LabelSelectorRequirement{}

	for _, m := range sel.MatchExpressions {
		matchExp := metav1.LabelSelectorRequirement{
			Key:      m.Key,
			Operator: metav1.LabelSelectorOperator(m.Operator),
			Values:   m.Values,
		}
		matchExp.Values = make([]string, len(m.Values))
		copy(matchExp.Values, m.Values)
		res.MatchExpressions = append(res.MatchExpressions, matchExp)
	}
	return res
}

func parseHoldTime(ht string) (time.Duration, error) {
	if ht == "" {
		return 90 * time.Second, nil
	}
	d, err := time.ParseDuration(ht)
	if err != nil {
		return 0, fmt.Errorf("invalid hold time %q: %s", ht, err)
	}
	rounded := time.Duration(int(d.Seconds())) * time.Second
	if rounded != 0 && rounded < 3*time.Second {
		return 0, fmt.Errorf("invalid hold time %q: must be 0 or >=3s", ht)
	}
	return rounded, nil
}
