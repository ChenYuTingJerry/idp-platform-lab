/*
Copyright 2026.

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

package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ensureFinalizer adds name to obj's finalizers if it is missing, using a merge
// patch WITH an optimistic-lock precondition. Unlike a status write (single
// writer, last-writer-wins), metadata.finalizers is multi-writer: kubectl, the
// garbage collector's foregroundDeletion, and other operators all touch it. A
// blind merge patch would replace the whole list and could clobber a finalizer
// added between our read and our write, so a Conflict here is correct and the
// caller should requeue.
func ensureFinalizer(ctx context.Context, c client.Client, obj client.Object, name string) error {
	if controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	base := client.MergeFromWithOptions(obj.DeepCopyObject().(client.Object), client.MergeFromWithOptimisticLock{})
	controllerutil.AddFinalizer(obj, name)
	return c.Patch(ctx, obj, base)
}

// releaseFinalizer removes name from obj's finalizers with the same optimistic
// lock. Callers must write status BEFORE releasing: once the last finalizer is
// gone the object can vanish, and a later write would 404.
func releaseFinalizer(ctx context.Context, c client.Client, obj client.Object, name string) error {
	if !controllerutil.ContainsFinalizer(obj, name) {
		return nil
	}
	base := client.MergeFromWithOptions(obj.DeepCopyObject().(client.Object), client.MergeFromWithOptimisticLock{})
	controllerutil.RemoveFinalizer(obj, name)
	return c.Patch(ctx, obj, base)
}
