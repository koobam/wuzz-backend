// Local demo outlets for the customer app's discovery (wuzz_catalog.outlets in
// MongoDB). Safe to re-run: each outlet is upserted by _id. Id 1 is the same
// outlet scripts/seed-local.sql puts in Postgres.
const wuzz = db.getSiblingDB('wuzz_catalog');
const photo = (id) => `https://images.unsplash.com/${id}?w=800`;
// Square crops for the service pictures (the checkout shows them small).
const servicePhoto = (id) => `https://images.unsplash.com/${id}?w=400&h=400&fit=crop`;
const now = new Date();

const outlets = [
  { _id: 1, name: 'Wuzz Pusat', address: 'Jl. Contoh No. 1, Jakarta', lat: -6.2088, lng: 106.8456, category: 'all', status: 'moderate', delivery: ['pickup', 'delivery'], open: '08:00', close: '22:00', images: [photo('photo-1442512595331-e89e73853f31')] },
  { _id: 2, name: 'Wuzz Station Blok M', address: 'Jl. Melawai Raya, Blok M, Jakarta Selatan', lat: -6.2440, lng: 106.7994, category: 'all', status: 'moderate', delivery: ['pickup', 'delivery'], open: '08:00', close: '22:00', images: [photo('photo-1509042239860-f550ce710b93')] },
  { _id: 3, name: 'Wuzz Sudirman', address: 'Jl. Jend. Sudirman Kav. 52, Jakarta Selatan', lat: -6.2250, lng: 106.8090, category: 'laundry', status: 'busy', delivery: ['pickup'], open: '07:00', close: '21:00', images: [photo('photo-1514432324607-a09d9b4aefdd')] },
  { _id: 4, name: 'Wuzz Kemang', address: 'Jl. Kemang Raya No. 8, Jakarta Selatan', lat: -6.2615, lng: 106.8135, category: 'caffe', status: 'quiet', delivery: ['pickup', 'delivery'], open: '09:00', close: '23:00', images: [photo('photo-1442512595331-e89e73853f31')] },
  { _id: 5, name: 'Wuzz Kelapa Gading', address: 'Jl. Boulevard Raya, Kelapa Gading, Jakarta Utara', lat: -6.1580, lng: 106.9080, category: 'all', status: 'quiet', delivery: ['pickup', 'delivery'], open: '08:00', close: '22:00', images: [photo('photo-1509042239860-f550ce710b93')] },
  { _id: 7, name: 'Wuzz Puri Indah', address: 'Jl. Puri Indah Raya, Kembangan, Jakarta Barat', lat: -6.1880, lng: 106.7370, category: 'all', status: 'quiet', delivery: ['pickup', 'delivery'], open: '09:00', close: '22:00', images: [photo('photo-1442512595331-e89e73853f31')] },
  { _id: 6, name: 'Wuzz Supermall Karawaci', address: 'Jl. Boulevard Diponegoro, Karawaci, Tangerang', lat: -6.2270, lng: 106.6090, category: 'laundry', status: 'moderate', delivery: ['delivery'], open: '10:00', close: '22:00', images: [photo('photo-1514432324607-a09d9b4aefdd')] },
];

wuzz.outlets.createIndex({ location: '2dsphere' });
wuzz.outlets.createIndex({ active: 1, name: 1 });

for (const o of outlets) {
  wuzz.outlets.updateOne(
    { _id: o._id },
    {
      $set: {
        name: o.name,
        address: o.address,
        category: o.category,
        location: { type: 'Point', coordinates: [o.lng, o.lat] },
        images: o.images,
        current_status: o.status,
        delivery_types: o.delivery,
        open_time: o.open,
        close_time: o.close,
        active: true,
        updated_at: now,
      },
      $setOnInsert: { created_at: now },
    },
    { upsert: true },
  );
}
print(`seeded ${outlets.length} outlets`);

// Services the app browses. Ids 1-5 are the same services seed-local.sql puts in
// Postgres (8 and up exist only here); `icon` is a name from the app's icon set.
const services = [
  { _id: 8, order: 1, name: 'Cuci Pakaian Kiloan', description: 'Cuci pakaian sehari-hari, dihitung per kilo', price: 7000, unit: 'kg', type: 'multiple', category: 'regular', min_kg: 3, max_kg: 20, icon: 'kiloan', image: servicePhoto('photo-1582735689369-4fe89db7114c') },
  { _id: 9, order: 2, name: 'Cuci Pakaian Satuan', description: 'Cuci per potong untuk pakaian pilihan', price: 12000, unit: 'pcs', type: 'single', category: 'regular', icon: 'fold' },
  { _id: 11, order: 3, name: 'Sepatu & Sandal', description: 'Sepatu dan sandal bersih dan wangi, dikerjakan dengan hati-hati', price: 35000, unit: 'pcs', type: 'single', category: 'regular', icon: 'wash', image: servicePhoto('photo-1491553895911-0055eca6402d') },
  { _id: 14, order: 4, name: 'Tas & Aksesoris', description: 'Tas, clutch, dompet, ransel, dan ikat pinggang bersih tanpa merusak bahan', price: 35000, unit: 'pcs', type: 'single', category: 'regular', icon: 'bag' },
  { _id: 10, order: 5, name: 'Perlengkapan Tidur & Mandi', description: 'Sprei, selimut, bantal, guling, handuk, dan jubah mandi bersih, kering, dan wangi', price: 28000, unit: 'pcs', type: 'single', category: 'regular', icon: 'bed' },
  { _id: 12, order: 6, name: 'Karpet & Gorden', description: 'Karpet, permadani, dan gorden bersih sampai ke serat', price: 45000, unit: 'pcs', type: 'single', category: 'regular', icon: 'fold' },
  { _id: 13, order: 7, name: 'Boneka & Mainan', description: 'Cuci boneka dan mainan kain dengan lembut', price: 20000, unit: 'pcs', type: 'single', category: 'regular', icon: 'bag' },
  { _id: 15, order: 8, name: 'Jaket & Topi', description: 'Jaket, hoodie, dan topi bersih tanpa merusak bentuk', price: 25000, unit: 'pcs', type: 'single', category: 'regular', icon: 'wash' },
  { _id: 17, order: 10, name: 'Mukena & Sajadah', description: 'Mukena dan sajadah dicuci hati-hati, rapi dan wangi', price: 18000, unit: 'pcs', type: 'single', category: 'regular', icon: 'fold' },
  { _id: 20, order: 11, name: 'Pakaian Formal', description: 'Jas, kebaya, dan dress dicuci lembut dengan tangan', price: 45000, unit: 'pcs', type: 'single', category: 'regular', icon: 'dry' },
  { _id: 21, order: 12, name: 'Perlengkapan Bayi', description: 'Baju, bedong, selimut, dan perlengkapan bayi dengan deterjen khusus yang lembut', price: 12000, unit: 'kg', type: 'multiple', category: 'regular', min_kg: 2, max_kg: 10, icon: 'wash' },
  { _id: 18, order: 13, name: 'Mobil & Motor', description: 'Cuci mobil atau motor, dikerjakan di tempatmu atau diantar ke gerai', price: 25000, unit: 'unit', type: 'single', category: 'regular', icon: 'wash', vehicle: true },
  { _id: 5, order: 1, name: 'Mesin Cuci', description: 'Cuci sendiri pakai mesin cuci di outlet', price: 15000, unit: 'sesi', type: 'single', category: 'alacarte', icon: 'wash', image: servicePhoto('photo-1604335399105-a0c585fd81a1') },
  { _id: 6, order: 2, name: 'Dryer', description: 'Keringkan sendiri pakai dryer di outlet', price: 10000, unit: 'sesi', type: 'single', category: 'alacarte', icon: 'dry' },
];

// Retired: Cuci + Lipat and Cuci + Setrika are now the finishing choice inside
// Cuci Pakaian Kiloan (see kiloanSections), not categories of their own. 3 and 4
// (setrika, dry clean) are dropped too: dry clean has no equipment and setrika
// is not offered as a category; 7 (Cuci Tas ala carte) is gone since Self Service is only Mesin Cuci and Dryer; 16 (Bantal & Guling) is folded into Perlengkapan Tidur & Mandi and 19 (Cuci Motor)
// into Mobil & Motor.
const retiredServiceIds = [1, 2, 3, 4, 7, 16, 19, 22];
for (const id of retiredServiceIds) {
  wuzz.services.updateOne({ _id: id }, { $set: { active: false, updated_at: now } });
}

wuzz.services.createIndex({ active: 1, service_category: 1, sort_order: 1 });

// Detail data (GET /app/v1/services/:id): tiers and add-on sections. Regular
// services get Reguler/Express tiers and add-ons; à-la-carte ones are flat.
const tiersFor = (sv) => [
  { id: 1, code: 'regular', name: 'Reguler', sort_order: 1, eta: sv.vehicle ? 3 : 2, mult: 1 },
  { id: 2, code: 'express', name: 'Express', sort_order: 2, eta: sv.vehicle ? 1 : 1, mult: 1.5 },
].map((t) => ({
  id: t.id,
  code: t.code,
  name: t.name,
  sort_order: t.sort_order,
  pricing: {
    id: sv._id * 10 + t.id,
    service_id: sv._id,
    tier_id: t.id,
    sku: `WZ-${sv._id}-${t.code.toUpperCase()}`,
    estimated_time: t.eta,
    estimated_time_unit: sv.vehicle ? 'jam' : 'hari',
    price: Math.round(sv.price * t.mult),
    discount: 0,
    discount_type: '',
    unit: sv.unit,
  },
}));

const sectionsFor = () => [
  {
    title: 'Pewangi',
    description: 'Pilih satu pewangi untuk cucianmu',
    type: 'single',
    addons: [
      { id: 1, name: 'Lavender', description: 'Aroma tenang dan segar', price: 0, unit: '', discount: 0, discount_type: '', icon: '' },
      { id: 2, name: 'Ocean Breeze', description: 'Aroma laut yang bersih', price: 2000, unit: 'kg', discount: 0, discount_type: '', icon: '' },
    ],
  },
  {
    title: 'Layanan Tambahan',
    description: 'Boleh pilih lebih dari satu',
    type: 'multiple',
    addons: [
      { id: 3, name: 'Packing Plastik', description: 'Dikemas rapi dalam plastik', price: 3000, unit: '', discount: 0, discount_type: '', icon: '' },
      { id: 4, name: 'Pisahkan Warna', description: 'Pakaian putih dan berwarna dicuci terpisah', price: 4000, unit: '', discount: 0, discount_type: '', icon: '' },
    ],
  },
];

// Mobil & Motor: one category; the vehicle type is a choice (motor is the base
// price, mobil adds to it), plus add-ons that make sense for a vehicle.
const vehicleSections = () => [
  {
    title: 'Jenis Kendaraan',
    description: 'Pilih satu',
    type: 'single',
    addons: [
      { id: 10, name: 'Motor', description: 'Semua ukuran motor', price: 0, unit: '', discount: 0, discount_type: '', icon: '' },
      { id: 11, name: 'Mobil', description: 'Sedan, SUV, dan MPV', price: 35000, unit: '', discount: 0, discount_type: '', icon: '' },
    ],
  },
  {
    title: 'Layanan Tambahan',
    description: 'Boleh pilih lebih dari satu',
    type: 'multiple',
    addons: [
      { id: 7, name: 'Cuci Interior', description: 'Vakum jok, dashboard, dan karpet dasar', price: 30000, unit: '', discount: 0, discount_type: '', icon: '' },
      { id: 8, name: 'Wax & Poles', description: 'Lapisan wax agar cat mengilap dan terlindungi', price: 40000, unit: '', discount: 0, discount_type: '', icon: '' },
      { id: 9, name: 'Cuci Mesin', description: 'Bersihkan ruang mesin dengan hati-hati', price: 35000, unit: '', discount: 0, discount_type: '', icon: '' },
    ],
  },
];

// Cuci Pakaian Kiloan: how the wash is finished is a choice here, not a separate service.
const kiloanSections = () => {
  const [pewangi, tambahan] = sectionsFor();
  return [
    {
      title: 'Penyelesaian',
      description: 'Pilih satu',
      type: 'single',
      addons: [
        { id: 5, name: 'Cuci + Lipat', description: 'Dicuci bersih lalu dilipat rapi', price: 0, unit: '', discount: 0, discount_type: '', icon: 'fold' },
        { id: 6, name: 'Cuci + Setrika', description: 'Dicuci bersih, disetrika licin', price: 3000, unit: 'kg', discount: 0, discount_type: '', icon: 'iron' },
      ],
    },
    pewangi,
    tambahan,
  ];
};

for (const sv of services) {
  wuzz.services.updateOne(
    { _id: sv._id },
    {
      $set: {
        name: sv.name,
        description: sv.description,
        price: sv.price,
        unit: sv.unit,
        service_type: sv.type,
        service_category: sv.category,
        min_kg: sv.min_kg ?? 0,
        max_kg: sv.max_kg ?? 0,
        discount: sv.discount ?? 0,
        discount_type: sv.discount_type ?? '',
        icon: sv.icon,
        image: sv.image ?? '',
        sort_order: sv.order,
        tiers: sv.category === 'regular' ? tiersFor(sv) : [],
        sections: sv.category !== 'regular' ? [] : sv._id === 8 ? kiloanSections() : sv.vehicle ? vehicleSections() : sectionsFor(),
        active: true,
        updated_at: now,
      },
      $setOnInsert: { created_at: now },
    },
    { upsert: true },
  );
}
print(`seeded ${services.length} services`);

// Service areas the customer app resolves a position into (GET /app/v1/coverage).
// DEMO SHAPES: coarse boxes sized to contain the demo outlets above, not real
// administrative boundaries. Replace with real polygons before using for real.
const box = (west, south, east, north) => ({
  type: 'Polygon',
  coordinates: [[[west, south], [east, south], [east, north], [west, north], [west, south]]],
});
const areas = [
  { _id: 1, name: 'Jakarta Barat', geometry: box(106.69, -6.215, 106.80, -6.11) },
  { _id: 2, name: 'Jakarta Pusat', geometry: box(106.80, -6.215, 106.88, -6.18) },
  { _id: 3, name: 'Jakarta Utara', geometry: box(106.80, -6.18, 106.95, -6.08) },
  { _id: 4, name: 'Jakarta Selatan', geometry: box(106.75, -6.37, 106.88, -6.215) },
  { _id: 5, name: 'Jakarta Timur', geometry: box(106.88, -6.37, 106.98, -6.18) },
  { _id: 6, name: 'Tangerang', geometry: box(106.55, -6.30, 106.69, -6.15) },
];

wuzz.coverage_areas.createIndex({ geometry: '2dsphere' });

for (const a of areas) {
  wuzz.coverage_areas.updateOne(
    { _id: a._id },
    { $set: { name: a.name, geometry: a.geometry, active: true, updated_at: now }, $setOnInsert: { created_at: now } },
    { upsert: true },
  );
}
print(`seeded ${areas.length} coverage areas`);
