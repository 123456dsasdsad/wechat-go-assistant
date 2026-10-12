<script setup>
defineProps({ name: String, size: { type: [Number, String], default: 20 } })
const paths = {
  grid: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  book: 'M12 5v16 M3 4c3-1 6 0 9 2 3-2 6-3 9-2v15c-3-1-6 0-9 2-3-2-6-3-9-2z',
  bulb: 'M9 18h6 M10 22h4 M8 14a7 7 0 1 1 8 0c-1 1-1 2-1 4H9c0-2 0-3-1-4z',
  flask: 'M9 3h6 M10 3v6L4 19q-1 2 2 2h12q3 0 2-2L14 9V3 M7 15h10',
  pen: 'm15 4 5 5 M4 15 15 4a3.5 3.5 0 0 1 5 5L9 20l-6 1z',
  review: 'M9 3H5v18h14V3h-4 M9 2h6v4H9z M8 11l2 2 5-5 M8 17h8',
  arrow: 'M5 12h14 m-6-6 6 6-6 6',
  chevron: 'm9 5 7 7-7 7',
  down: 'm6 9 6 6 6-6',
  search: 'M21 21l-5-5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  plus: 'M12 5v14 M5 12h14',
  close: 'm6 6 12 12 M18 6 6 18',
  check: 'm5 12 4 4L19 6',
  clock: 'M12 8v5l3 2 M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
  download: 'M12 3v12 m-5-5 5 5 5-5 M4 16v5h16v-5',
  settings: 'M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6',
  help: 'M9 8a3 3 0 1 1 5 2c-2 1-2 2-2 4 M12 17h.01 M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
  link: 'm10 14 4-4 M9 15l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0 M15 9l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0',
  file: 'M14 2H5v20h14V7z M14 2v5h5 M8 12h8 M8 16h6',
  sparkle: 'm12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5z',
  menu: 'M4 6h16 M4 12h16 M4 18h16',
  history: 'M3 11a9 9 0 1 1 2 7 M3 4v7h7 M12 7v6l4 2',
  focus: 'M8 3H3v5 M16 3h5v5 M21 16v5h-5 M3 16v5h5',
  chart: 'M4 3v18h17 M8 16v-5 M13 16V7 M18 16V4',
  alert: 'm12 3 10 18H2z M12 9v5 M12 17h.01',
}
</script>
<template><svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] || paths.file" /></svg></template>
