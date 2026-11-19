package helpers

import (
	"context"
	"fmt"

	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The attic: a deleted namespace's layout, kept in the system namespace as
// kubendt-internal-layout-<namespace> until a namespace of that name is
// created again and takes it back, or the user deletes it with the namespace.
const layoutAtticPrefix = "kubendt-internal-layout-"

func layoutAtticName(namespace string) string { return layoutAtticPrefix + namespace }

// StoreLayoutInAttic copies the layout of a namespace about to go into the
// system namespace. A namespace with no layout leaves nothing there.
func StoreLayoutInAttic(namespace string) error {
	l, err := GetLayout(namespace)
	if err != nil {
		return err
	}
	if len(l.Positions) == 0 && len(l.Annotations) == 0 {
		return nil
	}
	if err := EnsureSystemNamespace(); err != nil {
		return err
	}
	data, err := encodeLayout(l)
	if err != nil {
		return err
	}
	labels := internalLabels("layout-attic")
	labels["kubendt/namespace"] = namespace
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: layoutAtticName(namespace), Namespace: SystemNamespace(), Labels: labels},
		Data:       map[string]string{layoutKey: data},
	}
	cms := layoutConfigMaps(SystemNamespace())
	_, err = cms.Create(context.TODO(), cm, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = cms.Update(context.TODO(), cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("could not keep the layout of %s in the attic: %w", namespace, err)
	}
	return nil
}

// RestoreLayoutFromAttic gives a new namespace the layout its namesake left
// behind, and says whether there was one.
func RestoreLayoutFromAttic(namespace string) (bool, error) {
	cms := layoutConfigMaps(SystemNamespace())
	cm, err := cms.Get(context.TODO(), layoutAtticName(namespace), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not read the attic layout of %s: %w", namespace, err)
	}
	l := decodeLayout(cm)
	if l.Positions == nil {
		l.Positions = map[string]types.NodePosition{}
	}
	if l.Annotations == nil {
		l.Annotations = []types.Annotation{}
	}
	if err := PutLayout(namespace, l); err != nil {
		return false, err
	}
	if err := cms.Delete(context.TODO(), layoutAtticName(namespace), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return true, fmt.Errorf("the layout of %s is back but its attic copy stays: %w", namespace, err)
	}
	return true, nil
}

// DropLayoutAttic removes what the attic keeps for a namespace, if anything.
func DropLayoutAttic(namespace string) error {
	err := layoutConfigMaps(SystemNamespace()).Delete(context.TODO(), layoutAtticName(namespace), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not drop the attic layout of %s: %w", namespace, err)
	}
	return nil
}
