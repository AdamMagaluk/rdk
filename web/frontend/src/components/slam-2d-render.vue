<script setup lang="ts">

import { threeInstance, resizeRendererToDisplaySize } from 'trzy';
import { onMounted, onUnmounted, watch } from 'vue';
import * as THREE from 'three';
import { MapControls } from 'three/examples/jsm/controls/OrbitControls';
import { PCDLoader } from 'three/examples/jsm/loaders/PCDLoader';
import type { commonApi } from '@viamrobotics/sdk';

interface Props {
  name: string

  /*
   * NOTE: This is needed as vue doesn't support watchers for Uint8Array
   * so we use the pointCloudUpdateCount as a signal that the pointcloud
   * has changed & needs to be re rendered.
   */
  pointCloudUpdateCount: number
  resources: commonApi.ResourceName.AsObject[]
  pointcloud?: Uint8Array
  pose?: commonApi.Pose
}

const props = defineProps<Props>();

const loader = new PCDLoader();

const container = $ref<HTMLElement>();

const { scene, renderer } = threeInstance();

const color = new THREE.Color(0xFF_FF_FF);
renderer.setClearColor(color, 1);

renderer.domElement.style.cssText = 'width:100%;height:100%;';

const camera = new THREE.OrthographicCamera(-1, 1, 0.5, -0.5, -1, 1000);
camera.userData.size = 2;

const markerSize = 0.5;
const marker = new THREE.Mesh(
  new THREE.PlaneGeometry(markerSize, markerSize).rotateX(-Math.PI / 2),
  new THREE.MeshBasicMaterial({ color: 'red' })
);

const controls = new MapControls(camera, renderer.domElement);

const disposeScene = () => {
  scene.traverse((object: THREE.Points | THREE.Material | unknown) => {
    if (object instanceof THREE.Points) {
      object.geometry.dispose();

      if (object.material instanceof THREE.Material) {
        object.material.dispose();
      }
    }
  });

  scene.clear();
};

const update = (pointcloud: Uint8Array, pose: commonApi.Pose) => {
  const points = loader.parse(pointcloud.buffer, '');

  const x = pose.getX!();
  const y = pose.getY!();
  marker.position.setX(x);

  /*
   * TODO: This is set to xz b/c we are projecting on the xz plane.
   * This is temporary & will be changed to `marker.position.setZ(z);`
   * when the frontend is migrated to use GetPositionNew
   * Ticket: https://viam.atlassian.net/browse/RSDK-1066
   */
  marker.position.setZ(y);

  disposeScene();
  scene.add(points);
  scene.add(marker);
};

const init = (pointcloud: Uint8Array, pose: commonApi.Pose) => {
  update(pointcloud, pose);
};

onMounted(() => {
  container.append(renderer.domElement);

  camera.position.set(0, 100, 0);
  camera.lookAt(0, 0, 0);

  renderer.setAnimationLoop(() => {
    resizeRendererToDisplaySize(camera, renderer);

    renderer.render(scene, camera);
    controls.update();
  });

  if (props.pointcloud !== undefined && props.pose !== undefined) {
    init(props.pointcloud, props.pose);
  }
});

onUnmounted(() => {
  renderer.setAnimationLoop(null);
  disposeScene();
});

watch(
  [() => (props.pointCloudUpdateCount), () => (props.pose)],
  () => {
    if (props.pointcloud !== undefined && props.pose !== undefined) {
      init(props.pointcloud, props.pose);
    }
  }
);

</script>

<template>
  <div class="flex flex-col gap-4">
    <div
      ref="container"
      class="pcd-container relative w-full border border-black"
    />
  </div>
</template>
