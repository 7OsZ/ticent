import http from "k6/http";
import { check, sleep } from "k6";
import { Counter } from "k6/metrics";

// Load test skenario "war ticket":
// BUYERS pembeli berebut STOCK tiket di satu kategori pada detik yang sama lewat jalur HTTP penuh.
//
// Prasyarat:
// 1. Jalankan server terhadap PostgreSQL 18 lokal (bukan Neon) agar angka tidak tercampur latensi internet.
// 2. Buat 1 akun admin di DB lokal (role = 'admin'), lalu jalankan:
//    k6 run -e ADMIN_EMAIL=admin@ticent.local -e ADMIN_PASSWORD=password123 loadtest/war.js

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || "admin@ticent.local";
const ADMIN_PASSWORD = __ENV.ADMIN_PASSWORD || "password123";
const BUYERS = Number(__ENV.BUYERS || 200);
const STOCK = Number(__ENV.STOCK || 50);

const ordersCreated = new Counter("orders_created_201");
const ordersSoldOut = new Counter("orders_sold_out_409");
const unexpectedResponses = new Counter("unexpected_responses");

export const options = {
  setupTimeout: "3m",
  scenarios: {
    war_checkout: {
      executor: "per-vu-iterations",
      vus: BUYERS,
      iterations: 1,
      maxDuration: "1m",
    },
  },
  thresholds: {
    unexpected_responses: ["count==0"],
    orders_created_201: [`count==${STOCK}`],
    http_req_failed: ["rate<1"], // 409 kehabisan stok adalah respons bisnis yang diharapkan
  },
};

const jsonHeaders = (token) => ({
  headers: {
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  },
});

export function setup() {
  // 1) Login admin
  const adminRes = http.post(
    `${BASE_URL}/auth/login`,
    JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
    jsonHeaders()
  );
  if (adminRes.status !== 200) {
    throw new Error(`Login admin gagal (${adminRes.status}): ${adminRes.body}`);
  }
  const adminToken = adminRes.json("token");

  // 2) Daftarkan & login seluruh pembeli LEBIH DULU (bcrypt memakan waktu),
  //    supaya saat event dibuat semua pembeli bisa masuk waiting room SEBELUM jam war (diacak adil).
  const runTag = Date.now();
  const tokens = [];
  for (let i = 0; i < BUYERS; i++) {
    const email = `war.${runTag}.${i}@test.local`;
    const password = "password123";
    http.post(
      `${BASE_URL}/auth/register`,
      JSON.stringify({ email, full_name: `Buyer ${i}`, password }),
      jsonHeaders()
    );
    const loginRes = http.post(`${BASE_URL}/auth/login`, JSON.stringify({ email, password }), jsonHeaders());
    tokens.push(loginRes.json("token"));
  }

  // 3) Buat & publish event war (jam war 5 detik dari sekarang)
  const saleStartsAt = new Date(Date.now() + 5000).toISOString();
  const evRes = http.post(
    `${BASE_URL}/admin/events`,
    JSON.stringify({
      name: `War LoadTest ${runTag}`,
      venue: "GBK Jakarta",
      sale_starts_at: saleStartsAt,
      max_active_sessions: BUYERS,
      max_tickets_per_user: 4,
      session_minutes: 30,
    }),
    jsonHeaders(adminToken)
  );
  if (evRes.status !== 201) {
    throw new Error(`Buat event gagal (${evRes.status}): ${evRes.body}`);
  }
  const eventID = evRes.json("id");

  // Tanggal pertunjukan = besok (aman di zona WIB)
  const tomorrow = new Date(Date.now() + 24 * 3600 * 1000).toISOString().slice(0, 10);
  const dayRes = http.post(
    `${BASE_URL}/admin/events/${eventID}/days`,
    JSON.stringify({ label: "DAY-1", show_date: tomorrow }),
    jsonHeaders(adminToken)
  );
  const dayID = dayRes.json("id");

  const catRes = http.post(
    `${BASE_URL}/admin/days/${dayID}/categories`,
    JSON.stringify({ name: "Festival", price: 500000, total_seats: STOCK, has_seats: false }),
    jsonHeaders(adminToken)
  );
  const categoryID = catRes.json("id");

  const pubRes = http.post(`${BASE_URL}/admin/events/${eventID}/publish`, null, jsonHeaders(adminToken));
  if (pubRes.status !== 200) {
    throw new Error(`Publish gagal (${pubRes.status}): ${pubRes.body}`);
  }

  // 4) Masukkan semua pembeli ke waiting room sebelum jam war, lalu tunggu worker meloloskan semuanya
  for (const tok of tokens) {
    http.post(`${BASE_URL}/events/${eventID}/queue`, null, jsonHeaders(tok));
  }
  sleep(8);

  return { eventID, dayID, categoryID, tokens };
}

export default function (data) {
  const token = data.tokens[__VU - 1];
  const res = http.post(
    `${BASE_URL}/orders`,
    JSON.stringify({
      event_day_id: data.dayID,
      items: [{ category_id: data.categoryID, qty: 1 }],
    }),
    jsonHeaders(token)
  );

  if (res.status === 201) {
    ordersCreated.add(1);
  } else if (res.status === 409) {
    ordersSoldOut.add(1);
  } else {
    unexpectedResponses.add(1);
  }

  check(res, {
    "status adalah 201 (dapat tiket) atau 409 (habis)": (r) => r.status === 201 || r.status === 409,
  });
}

export function teardown(data) {
  const catRes = http.get(`${BASE_URL}/catalog/${data.dayID}`);
  if (catRes.status === 200) {
    const categories = catRes.json("categories") || [];
    const available = categories.length ? categories[0].available : -1;
    console.log(`Stok akhir kategori setelah war: available = ${available} (target: 0, oversell: 0)`);
  }
}
