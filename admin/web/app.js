"use strict";

/* ---------- State & helpers ---------- */
const SECRET_KEY = "wfc_admin_secret";
const PANEL_VIEWS = new Set(["dashboard", "rooms", "races", "players", "bans", "settings"]);
const REFRESH_INTERVALS = new Set([0, 3, 5, 10, 30, 60]);
const state = {
	secret: localStorage.getItem(SECRET_KEY) || "", // holds a session token once logged in
	view: "dashboard",
	config: null,
	preferences: { lastView: "dashboard", refreshSeconds: 3 },
	preferencesLoaded: false,
	preferenceSave: Promise.resolve(),
	timer: null,
	live: {
		races: [], selectedId: "", selectedGroup: "", timer: null, ageTimer: null, controller: null,
		generation: 0, auto: null, failed: false, error: "", receivedAt: 0, available: null,
		maps: new Map(), optionsKey: "",
	},
};

const $ = (sel, root = document) => root.querySelector(sel);
const esc = (s) =>
	String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
		({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
	);
const fmtFC = (fc) =>
	/^\d{12}$/.test(fc) ? fc.replace(/(\d{4})(\d{4})(\d{4})/, "$1-$2-$3") : fc || "—";
const fmtTime = (t) => {
	if (!t) return "—";
	const d = new Date(t);
	if (isNaN(d) || d.getFullYear() < 2001) return "—";
	return d.toLocaleString();
};
const fmtRemaining = (t) => {
	if (!t) return "permanent";
	const ms = new Date(t) - Date.now();
	if (ms <= 0) return "expired";
	const days = Math.floor(ms / 86400000);
	const hrs = Math.floor((ms % 86400000) / 3600000);
	if (days > 365 * 50) return "permanent";
	if (days > 0) return `${days}d ${hrs}h left`;
	const mins = Math.floor((ms % 3600000) / 60000);
	return hrs > 0 ? `${hrs}h ${mins}m left` : `${mins}m left`;
};

/* Mario Kart Wii course IDs, indexed by the in-match course id the server
   reports (wl:mkw_select_course). Matches common.MarioKartWiiCourseId. */
const COURSES = [
	"Mario Circuit", "Moo Moo Meadows", "Mushroom Gorge", "Grumble Volcano",
	"Toad's Factory", "Coconut Mall", "DK Summit", "Wario's Gold Mine",
	"Luigi Circuit", "Daisy Circuit", "Moonview Highway", "Maple Treeway",
	"Bowser's Castle", "Rainbow Road", "Dry Dry Ruins", "Koopa Cape",
	"GCN Peach Beach", "GCN Mario Circuit", "GCN Waluigi Stadium", "GCN DK Mountain",
	"DS Yoshi Falls", "DS Desert Hills", "DS Peach Gardens", "DS Delfino Square",
	"SNES Mario Circuit 3", "SNES Ghost Valley 2", "N64 Mario Raceway", "N64 Sherbet Land",
	"N64 Bowser's Castle", "N64 DK's Jungle Parkway", "GBA Bowser Castle 3", "GBA Shy Guy Beach",
];
const courseName = (id) => (id >= 0 && id < COURSES.length ? COURSES[id] : `course ${id}`);
const CC = { 0: "50cc", 1: "100cc", 2: "150cc", 3: "Mirror" };
const ccName = (id) => CC[id] || `cc ${id}`;
const KNOCKDOWN_POOL_REGION = "vs_19279";

/* ---------- API ---------- */
async function handle(res) {
	if (res.status === 401) {
		const e = new Error("Authentication failed");
		e.auth = true;
		throw e;
	}
	const txt = await res.text();
	let data = null;
	if (txt) {
		try { data = JSON.parse(txt); } catch { data = txt; }
	}
	if (!res.ok) {
		const e = new Error((data && data.error) || "HTTP " + res.status);
		e.data = data;
		throw e;
	}
	return data;
}
async function apiGet(path, params = {}, options = {}) {
	const u = new URL(path, location.origin);
	u.searchParams.set("secret", state.secret);
	for (const [k, v] of Object.entries(params)) u.searchParams.set(k, v);
	return handle(await fetch(u, { headers: { Accept: "application/json" }, signal: options.signal }));
}
async function apiPost(path, body = {}) {
	return handle(
		await fetch(path, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ secret: state.secret, ...body }),
		})
	);
}

/* ---------- Toast ---------- */
function toast(msg, kind = "") {
	const t = document.createElement("div");
	t.className = "toast " + kind;
	t.textContent = msg;
	$("#toast-root").appendChild(t);
	setTimeout(() => {
		t.style.opacity = "0";
		t.style.transition = "opacity .3s";
		setTimeout(() => t.remove(), 300);
	}, 3200);
}
function onError(e) {
	if (e && e.auth) {
		stopPolling();
		showGate("Session expired. Please sign in again.");
		return;
	}
	toast((e && e.message) || "Request failed", "bad");
}

/* ---------- Gate ---------- */
function showGate(err = "") {
	stopLiveRacePolling();
	state.live.races = [];
	state.live.maps.clear();
	state.live.selectedId = "";
	state.live.selectedGroup = "";
	state.live.auto = null;
	state.live.failed = false;
	state.live.error = "";
	state.live.available = null;
	$("#app").classList.add("hidden");
	$("#gate").classList.remove("hidden");
	$("#gate-error").textContent = err;
	$("#gate-secret").value = "";
	$("#gate-secret").focus();
}
function showApp() {
	$("#gate").classList.add("hidden");
	$("#app").classList.remove("hidden");
}
async function enterApp() {
	state.config = await apiGet("/api/config");
	state.preferencesLoaded = false;
	try {
		const preferences = await apiGet("/api/preferences");
		if (!preferences || !PANEL_VIEWS.has(preferences.lastView) || !REFRESH_INTERVALS.has(preferences.refreshSeconds)) {
			throw new Error("Invalid panel preferences");
		}
		state.preferences = preferences;
		state.preferencesLoaded = true;
	} catch (err) {
		if (err.auth) throw err;
		// Keep a valid login usable when optional display preferences fail.
		// Do not save defaults over a store that could not be read.
	}
	showApp();
	setView(state.preferencesLoaded ? state.preferences.lastView : "dashboard", false);
	startPolling();
	if (!state.preferencesLoaded) toast("Panel preferences could not be loaded. Server controls are still available.", "bad");
}
function savePanelPreferences(changes) {
	const token = state.secret;
	state.preferenceSave = state.preferenceSave.catch(() => {}).then(() => {
		if (!state.preferencesLoaded || !token || state.secret !== token) return;
		return apiPost("/api/preferences", changes);
	});
	return state.preferenceSave;
}
$("#gate-form").addEventListener("submit", async (e) => {
	e.preventDefault();
	const field = $("#gate-secret").value;
	if (!field) return;
	try {
		const resp = await apiPost("/api/login", { password: field });
		state.secret = resp.token;
		if ($("#gate-remember").checked) localStorage.setItem(SECRET_KEY, state.secret);
		else localStorage.removeItem(SECRET_KEY);
		await enterApp();
	} catch (err) {
		state.secret = "";
		$("#gate-error").textContent = err.auth ? "Wrong password." : "Could not reach server.";
	}
});
$("#btn-logout").addEventListener("click", async () => {
	try { await apiPost("/api/logout", {}); } catch { /* best effort */ }
	localStorage.removeItem(SECRET_KEY);
	state.secret = "";
	stopPolling();
	showGate("Locked.");
});

/* ---------- Tabs ---------- */
$("#tabs").addEventListener("click", (e) => {
	const btn = e.target.closest(".tab");
	if (!btn) return;
	setView(btn.dataset.view);
});
function setView(view, persist = true) {
	if (!PANEL_VIEWS.has(view)) return;
	stopLiveRacePolling();
	state.view = view;
	if (persist && state.preferencesLoaded) {
		state.preferences.lastView = view;
		savePanelPreferences({ lastView: view }).catch(onError);
	}
	for (const t of document.querySelectorAll(".tab")) {
		const active = t.dataset.view === view;
		t.classList.toggle("active", active);
		if (active) t.setAttribute("aria-current", "page");
		else t.removeAttribute("aria-current");
	}
	render();
	if (state.preferences.refreshSeconds === 0) refreshStats();
}

/* ---------- Polling ---------- */
const POLLED = new Set(["dashboard", "rooms", "players"]);
function startPolling() {
	if (state.timer) clearInterval(state.timer);
	state.timer = null;
	refreshStats();
	if (state.view === "races" && !state.live.controller && !state.live.timer) startLiveRacePolling();
	if (state.preferences.refreshSeconds === 0) return;
	state.timer = setInterval(() => {
		refreshStats();
		if (POLLED.has(state.view)) render();
	}, state.preferences.refreshSeconds * 1000);
}
function stopPolling() {
	if (state.timer) clearInterval(state.timer);
	state.timer = null;
	stopLiveRacePolling();
}
async function refreshStats() {
	try {
		const stats = await apiGet("/api/stats");
		const g = (stats && stats.global) || { online: 0, groups: 0 };
		$("#stat-online").textContent = g.online || 0;
		$("#stat-rooms").textContent = g.groups || 0;
		$("#conn-dot").className = "pill-dot ok";
		$("#conn-text").textContent = "connected";
	} catch (e) {
		$("#conn-dot").className = "pill-dot err";
		$("#conn-text").textContent = "offline";
		if (e.auth) { stopPolling(); showGate("Session expired. Please sign in again."); }
	}
}

/* ---------- Render dispatch ---------- */
function render() {
	switch (state.view) {
		case "dashboard": return renderDashboard();
		case "rooms": return renderRooms();
		case "races": return renderLiveRaces();
		case "players": return renderPlayers();
		case "bans": return renderBans();
		case "settings": return renderSettings();
	}
}

/* ---------- Dashboard ---------- */
async function renderDashboard() {
	try {
		const [stats, cfg] = await Promise.all([apiGet("/api/stats"), apiGet("/api/config")]);
		state.config = cfg;
		$("#server-name").textContent = cfg.serverName || "mkwfc";
		const g = (stats && stats.global) || { online: 0, active: 0, groups: 0 };
		const games = Object.entries(stats || {}).filter(([k]) => k !== "global");

		if (state.view !== "dashboard") return; // a newer view won the race
		$("#content").innerHTML = `
			<div class="page-heading">
				<span class="heading-orb"><svg class="icon" aria-hidden="true"><use href="#icon-dashboard"/></svg></span>
				<div><h1>Server overview</h1><p>Players, rooms, and your local WFC server.</p></div>
			</div>
			<div class="grid">
				<div class="card stat-card"><div><div class="stat-num accent">${g.online || 0}</div><div class="stat-label">Players online</div></div><span class="stat-orb green"><svg class="icon" aria-hidden="true"><use href="#icon-players"/></svg></span></div>
				<div class="card stat-card"><div><div class="stat-num blue">${g.active || 0}</div><div class="stat-label">In a race / match</div></div><span class="stat-orb"><svg class="icon" aria-hidden="true"><use href="#icon-race"/></svg></span></div>
				<div class="card stat-card"><div><div class="stat-num">${g.groups || 0}</div><div class="stat-label">Active rooms</div></div><span class="stat-orb"><svg class="icon" aria-hidden="true"><use href="#icon-rooms"/></svg></span></div>
			</div>
			${games.length ? `<div class="section-title">By game</div><div class="grid">${games.map(([name, s]) =>
				`<div class="card"><b>${esc(name)}</b><div class="info-row"><span class="k">Online</span><span class="v">${s.online}</span></div><div class="info-row"><span class="k">Rooms</span><span class="v">${s.groups}</span></div></div>`
			).join("")}</div>` : ""}
			<div class="section-title">Server</div>
			<div class="card"><div class="info-grid">
				${infoRow("Name", cfg.serverName)}
				${infoRow("Admin panel", cfg.adminAddress)}
				${infoRow("GameSpy bind", cfg.gsAddress)}
				${infoRow("NAS (HTTP)", cfg.nasAddress + ":" + cfg.nasPort)}
				${infoRow("HTTPS", (cfg.enableHttps ? "on :" + cfg.nasPortHttps : "off"))}
				${infoRow("Wii HTTPS exploit", cfg.enableHttpsExploitWii ? "on" : "off")}
				${infoRow("Device-key policy", cfg.allowMultipleDeviceIDs)}
				${infoRow("Database (SQLite)", cfg.databasePath)}
			</div></div>`;
	} catch (e) { onError(e); }
}
const infoRow = (k, v) => `<div class="info-row"><span class="k">${esc(k)}</span><span class="v">${esc(v)}</span></div>`;

/* ---------- Rooms ---------- */
async function renderRooms() {
	try {
		const groups = await apiGet("/api/groups");
		const list = Array.isArray(groups) ? groups : [];
		if (state.view !== "rooms") return;
		if (!list.length) {
			$("#content").innerHTML = `<div class="toolbar"><h2 style="margin:0">Rooms</h2></div><div class="card empty">No active rooms right now.</div>`;
			return;
		}
		$("#content").innerHTML =
			`<div class="toolbar"><h2 style="margin:0">Rooms</h2><span class="tag gray">${list.length}</span></div>` +
			list.map(roomCard).join("");
	} catch (e) { onError(e); }
}
function roomCard(gr) {
	const players = Object.values(gr.players || {});
	const typeTag = gr.type === "private" ? "yellow" : gr.type === "anybody" ? "green" : "gray";
	const knockdownPool = gr.game === "mariokartwii" && gr.type === "anybody" && gr.rk === KNOCKDOWN_POOL_REGION;
	return `<div class="room">
		<div class="room-head">
			<span class="rid">${esc(gr.id)}</span>
			<span class="tag ${typeTag}">${esc(gr.type || "?")}</span>
			<span class="tag blue">${esc(gr.game)}</span>
			${gr.rk ? `<span class="tag ${knockdownPool ? "yellow" : "gray"}">${knockdownPool ? "Knockdown pool" : esc(gr.rk)}</span>` : ""}
			${gr.race ? `<span class="tag blue">🏁 ${esc(courseName(gr.race.course))}</span><span class="tag gray">${esc(ccName(gr.race.cc))} · race ${esc(gr.race.num)}</span>` : ""}
			<span class="spacer"></span>
			<span class="tag ${gr.suspend ? "gray" : "green"}">${gr.suspend ? "idle" : "live"}</span>
			<span class="tag gray">${players.length} 👥</span>
		</div>
		<div class="room-players table-wrap" style="border:none">
			<table><thead><tr><th>Player</th><th>Friend code</th><th>PID</th><th>VR</th><th>Actions</th></tr></thead>
			<tbody>${players.map((p) => `
				<tr>
					<td>${esc(p.name) || "<span class='tag gray'>no name</span>"}</td>
					<td class="mono">${fmtFC(p.fc)}</td>
					<td class="mono">${esc(p.pid)}</td>
					<td class="mono">${esc(p.ev || p.eb || "—")}</td>
					<td class="row-actions">
						<button class="btn btn-sm" data-act="profile" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}">Profile</button>
						<button class="btn btn-sm" data-act="kick" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}">Kick</button>
						<button class="btn btn-sm btn-danger" data-act="ban" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}">Ban</button>
					</td>
				</tr>`).join("")}</tbody></table>
		</div>
	</div>`;
}

/* ---------- Live races ---------- */
const RACER_COLORS = ["#126da5", "#b13a41", "#548124", "#8c4baf", "#a56211", "#087b7c", "#b13878", "#4356b1", "#765331", "#52701b", "#6f5990", "#406c7d"];
const liveAutoEnabled = () => !state.live.failed && (state.live.auto === true || (state.live.auto !== false && state.preferences.refreshSeconds !== 0));
const liveViewVisible = () => state.view === "races" && !!state.secret && !document.hidden && !$("#app").classList.contains("hidden");
const raceMarkerNumber = (racer) => Number.isInteger(racer.id) && racer.id >= 0 && racer.id < 12 ? racer.id + 1 : "?";
const racerColor = (racer) => RACER_COLORS[Number.isInteger(racer.id) && racer.id >= 0 && racer.id < 12 ? racer.id : 0];
const raceRacerName = (racer) => racer.name || (racer.isCPU ? `CPU ${raceMarkerNumber(racer)}` : `Player ${raceMarkerNumber(racer)}`);
const validMapPoint = (point) => point && typeof point.x === "number" && typeof point.z === "number" && Number.isFinite(point.x) && Number.isFinite(point.z) && Math.abs(point.x) <= 1e7 && Math.abs(point.z) <= 1e7;

function stopLiveRacePolling() {
	if (state.live.timer) clearTimeout(state.live.timer);
	state.live.timer = null;
	if (state.live.ageTimer) clearTimeout(state.live.ageTimer);
	state.live.ageTimer = null;
	if (state.live.controller) state.live.controller.abort();
	state.live.controller = null;
	state.live.generation++;
}
function startLiveRacePolling() {
	stopLiveRacePolling();
	if (!liveViewVisible()) return;
	const generation = state.live.generation;
	startLiveRaceAgeClock();
	fetchLiveRaces(generation);
}
async function fetchLiveRaces(generation) {
	if (!liveViewVisible() || generation !== state.live.generation || state.live.controller) return;
	const controller = new AbortController();
	state.live.controller = controller;
	let timedOut = false;
	const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, 8000);
	try {
		const result = await apiGet("/api/races", {}, { signal: controller.signal });
		if (!liveViewVisible() || generation !== state.live.generation) return;
		if (!result || !Array.isArray(result.races)) throw new Error("Invalid live race response");
		state.live.available = result.available !== false;
		state.live.races = state.live.available ? result.races.filter((race) => race && typeof race.id === "string" && race.id).slice(0, 64) : [];
		if (!state.live.available) state.live.auto = false;
		state.live.receivedAt = performance.now();
		state.live.error = "";
		const activeIds = new Set(state.live.races.map((race) => race.id));
		for (const id of state.live.maps.keys()) if (!activeIds.has(id)) state.live.maps.delete(id);
		for (const race of state.live.races) updateLiveMapHistory(race);
		paintLiveRaces();
	} catch (err) {
		if ((err.name === "AbortError" && !timedOut) || generation !== state.live.generation) return;
		if (err.auth) { onError(err); return; }
		state.live.failed = true;
		state.live.auto = false;
		state.live.error = "Could not refresh live races. Resume live or refresh once to retry.";
		if (liveViewVisible()) paintLiveRaces();
	} finally {
		clearTimeout(timeout);
		if (state.live.controller === controller) state.live.controller = null;
		if (generation === state.live.generation && liveViewVisible() && liveAutoEnabled()) {
			state.live.timer = setTimeout(() => {
				state.live.timer = null;
				fetchLiveRaces(generation);
			}, 1000);
		}
	}
}
function renderLiveRaces() {
	state.live.optionsKey = "";
	$("#content").innerHTML = `
		<div class="page-heading">
			<span class="heading-orb"><svg class="icon" aria-hidden="true"><use href="#icon-race"/></svg></span>
			<div><h1>Live races</h1><p>Follow the field, from the starting grid to the finish.</p></div>
		</div>
		<div class="card live-controls">
			<div class="field live-room-field"><label for="live-room">Room</label><select id="live-room"><option>Waiting for a race…</option></select></div>
			<div class="live-buttons"><button type="button" class="btn btn-primary" id="live-toggle">Pause live</button><button type="button" class="btn" id="live-refresh">Refresh once</button></div>
			<p class="hint live-refresh-note" id="live-refresh-note"></p>
		</div>
		<div class="note hidden live-error" id="live-error" role="alert"></div>
		<div id="live-race-body"></div>`;
	$("#live-room").addEventListener("change", (event) => {
		state.live.selectedId = event.target.value;
		const race = state.live.races.find((entry) => entry.id === state.live.selectedId);
		state.live.selectedGroup = race ? String(race.groupId) : "";
		paintLiveRaces();
	});
	$("#live-toggle").addEventListener("click", () => {
		state.live.auto = !liveAutoEnabled();
		state.live.failed = false;
		state.live.error = "";
		stopLiveRacePolling();
		paintLiveRaces();
		if (liveAutoEnabled()) startLiveRacePolling();
	});
	$("#live-refresh").addEventListener("click", () => {
		state.live.failed = false;
		state.live.error = "";
		startLiveRacePolling();
	});
	paintLiveRaces();
	startLiveRacePolling();
}
function selectedLiveRace() {
	let race = state.live.races.find((entry) => entry.id === state.live.selectedId);
	if (!race && state.live.selectedGroup) race = state.live.races.find((entry) => String(entry.groupId) === state.live.selectedGroup);
	if (!race) race = state.live.races[0];
	state.live.selectedId = race ? race.id : "";
	if (race) state.live.selectedGroup = String(race.groupId);
	return race;
}
function liveRaceAge(race) {
	return Math.max(0, Number(race.ageMs) || 0) + Math.max(0, performance.now() - state.live.receivedAt);
}
function startLiveRaceAgeClock() {
	if (state.live.ageTimer || !liveViewVisible() || !state.live.races.length) return;
	state.live.ageTimer = setTimeout(() => {
		state.live.ageTimer = null;
		if (!liveViewVisible()) return;
		paintLiveRaceAge();
		startLiveRaceAgeClock();
	}, 1000);
}
function paintLiveRaceAge() {
	const race = state.live.races.find((entry) => entry.id === state.live.selectedId);
	if (!race) return;
	const age = liveRaceAge(race);
	const stale = race.stale || age >= 3000;
	const phase = $("#live-phase-tag");
	const delayed = $("#live-delay-tag");
	const seen = $("#live-seen-time");
	if (phase) phase.className = `tag ${stale ? "gray" : "green"}`;
	if (delayed) delayed.classList.toggle("hidden", !stale);
	if (seen) seen.textContent = new Date(Date.now() - age).toLocaleTimeString();
}
function racePhaseLabel(phase) {
	return ({ waiting: "Waiting", loading: "Loading", countdown: "Starting", racing: "Racing", results: "Results", finished: "Results" })[phase] || "Waiting";
}
function formatRaceFinish(ms) {
	if (!Number.isFinite(ms) || ms <= 0) return "—";
	return `${Math.floor(ms / 60000)}:${String(Math.floor(ms / 1000) % 60).padStart(2, "0")}.${String(Math.floor(ms) % 1000).padStart(3, "0")}`;
}
function liveRacerStatus(racer, phase, knockoutMode) {
	if (racer.eliminated) {
		const label = knockoutMode === "race" ? "Out of series · Spectating" : "Eliminated · Spectating";
		return `<span class="tag red">${label}</span>${racer.disconnected ? ' <span class="tag gray">Disconnected</span>' : ""}`;
	}
	if (racer.disconnected) return '<span class="tag gray">Disconnected</span>';
	if (racer.winner) return '<span class="tag green">Winner</span>';
	if (racer.finished) return '<span class="tag green">Finished</span>';
	if (phase === "waiting" || phase === "loading" || phase === "countdown") return '<span class="tag gray">On grid</span>';
	return '<span class="tag blue">Racing</span>';
}
function liveKnockoutModeLabel(race) {
	if (!race.knockoutEnabled) return "";
	return race.knockoutMode === "race" ? "Race Knockout" : race.knockoutMode === "round" ? "Round Knockdown" : "Knockout";
}
function paintLiveRaces() {
	if (state.view !== "races" || !$("#live-race-body")) return;
	const race = selectedLiveRace();
	const selector = $("#live-room");
	const options = state.live.races.map((entry) => `<option value="${esc(entry.id)}">${esc(entry.roomName || `Room ${entry.groupId}`)}${entry.knockdownPool ? " · Knockdown pool" : ""}${entry.knockoutEnabled ? ` · ${liveKnockoutModeLabel(entry)}` : ""} · ${esc(courseName(entry.courseId))}</option>`).join("") || '<option value="">No race available</option>';
	if (state.live.optionsKey !== options) {
		selector.innerHTML = options;
		state.live.optionsKey = options;
	}
	selector.value = state.live.selectedId;
	selector.disabled = !state.live.races.length;
	const running = liveAutoEnabled();
	$("#live-toggle").textContent = running ? "Pause live" : "Resume live";
	$("#live-toggle").setAttribute("aria-pressed", String(running));
	$("#live-refresh-note").textContent = running ? "Live updates every second while this tab is visible." : "Live updates paused. Refresh once for a new snapshot, or resume live.";
	$("#live-error").textContent = state.live.error;
	$("#live-error").classList.toggle("hidden", !state.live.error);
	if (!race) {
		const unavailable = state.live.available === false;
		$("#live-race-body").innerHTML = `<div class="card live-empty"><span class="live-empty-orb"><svg class="icon" aria-hidden="true"><use href="#icon-race"/></svg></span><h2>${unavailable ? "Live races unavailable" : "Waiting for the starting grid"}</h2><p>${unavailable ? "This server build does not include a compatible game payload for live race reporting." : "Start an online VS race with the current compatible PAL game payload. The leaderboard and map will appear here when the host reports the race."}</p></div>`;
		return;
	}
	const racers = (Array.isArray(race.racers) ? race.racers : []).filter((racer) => racer && Number.isInteger(racer.id) && racer.id >= 0 && racer.id < 12).slice(0, 12);
	const sorted = [...racers].sort((a, b) => Number(!!b.winner) - Number(!!a.winner) || Number(!!a.eliminated) - Number(!!b.eliminated) || (Number(a.position) > 0 ? a.position : 99) - (Number(b.position) > 0 ? b.position : 99) || (Number(b.progress) || 0) - (Number(a.progress) || 0) || a.id - b.id);
	const age = liveRaceAge(race);
	const stale = race.stale || age >= 3000;
	const seen = new Date(Date.now() - age).toLocaleTimeString();
	const laps = Number.isInteger(race.laps) && race.laps > 0 ? race.laps : 0;
	const seriesRound = Number.isInteger(race.seriesRound) && race.seriesRound > 0 && race.seriesRound <= 12 ? race.seriesRound : 0;
	const outCount = [1, 2].includes(race.knockoutEliminationCount) ? race.knockoutEliminationCount : 0;
	const outLabel = outCount === 2 ? "Up to 2" : "1";
	const rows = sorted.map((racer) => `<tr class="${racer.eliminated ? "racer-eliminated " : ""}${racer.eliminated || racer.disconnected ? "racer-inactive" : ""}">
		<td class="race-place">${Number.isInteger(racer.position) && racer.position > 0 && racer.position <= 12 ? racer.position : "—"}</td>
		<th scope="row"><span class="racer-identity"><span class="racer-number" style="--racer-color:${racerColor(racer)}">${raceMarkerNumber(racer)}</span><span class="racer-name">${esc(raceRacerName(racer))}</span></span></th>
		<td><span class="tag ${racer.isCPU ? "gray" : "blue"}">${racer.isCPU ? "CPU" : "Player"}</span></td>
		<td class="mono">${Number.isInteger(racer.lap) && racer.lap > 0 ? `${laps ? Math.min(racer.lap, laps) : racer.lap}${laps ? ` / ${laps}` : ""}` : "—"}</td>
		<td>${liveRacerStatus(racer, race.phase, race.knockoutMode)}</td><td class="mono">${racer.finished ? formatRaceFinish(racer.finishMs) : "—"}</td>
	</tr>`).join("");
	$("#live-race-body").innerHTML = `
		<div class="live-race-heading"><div><span class="live-eyebrow">${esc(race.roomName || `Room ${race.groupId}`)}</span><h2>${esc(courseName(race.courseId))}</h2></div>
			<div class="live-race-tags"><span class="tag ${stale ? "gray" : "green"}" id="live-phase-tag">${racePhaseLabel(race.phase)}</span><span class="tag yellow${stale ? "" : " hidden"}" id="live-delay-tag">Updates delayed</span>${race.mirror ? '<span class="tag blue">Mirror</span>' : ""}${race.knockdownPool ? '<span class="tag yellow">Knockdown pool</span>' : ""}${race.knockoutEnabled ? `<span class="tag yellow">${liveKnockoutModeLabel(race)}</span>` : ""}${race.knockoutMode === "race" && seriesRound ? `<span class="tag blue">Series race ${seriesRound}</span>` : ""}${race.knockoutMode === "race" && outCount ? `<span class="tag gray">${outLabel} out after this race</span>` : ""}${race.knockoutEnabled && Number.isInteger(race.remaining) ? `<span class="tag blue">${race.remaining} / ${racers.length} remaining</span>` : ""}<span class="tag gray">${racers.filter((racer) => !racer.isCPU).length} players · ${racers.filter((racer) => racer.isCPU).length} CPUs</span>${Number.isFinite(race.elapsedMs) && race.elapsedMs > 0 ? `<span class="tag blue">${formatRaceFinish(race.elapsedMs)}</span>` : ""}</div>
		</div>
		<div class="live-layout">
			<section class="live-leaderboard" aria-labelledby="live-leaderboard-title"><div class="section-title" id="live-leaderboard-title">Leaderboard</div><div class="table-wrap"><table class="race-table"><caption class="sr-only">${esc(courseName(race.courseId))} leaderboard. Numbered racer markers match the course map.</caption><thead><tr><th scope="col">Place</th><th scope="col">Racer</th><th scope="col">Type</th><th scope="col">Lap</th><th scope="col">Status</th><th scope="col">Finish</th></tr></thead><tbody>${rows || '<tr><td colspan="6" class="empty">Waiting for racer positions.</td></tr>'}</tbody></table></div><p class="hint live-seen">${running ? "Last report" : "Snapshot"}: <span id="live-seen-time">${esc(seen)}</span>. Numbered markers match the map.</p></section>
			<section class="live-map-section" aria-labelledby="live-map-title"><div class="section-title" id="live-map-title">Live course map</div><div class="card live-map-card">${liveCourseMap(race, racers)}<p class="hint live-map-note">${liveMapNote(race.map)}</p></div></section>
		</div>`;
	startLiveRaceAgeClock();
}
function liveMapNote(map) {
	if (map && map.complete && Array.isArray(map.points) && map.points.length) return "Course path from the current race. Numbered markers match the leaderboard.";
	if (map && map.kind === "enemy-path" && !map.complete) return "Course path is loading. Showing reported racer positions and recent movement.";
	return "Course path unavailable. Showing reported racer positions and recent movement.";
}
function updateLiveMapHistory(race) {
	let history = state.live.maps.get(race.id);
	if (!history) {
		history = { bounds: null, trails: new Map() };
		state.live.maps.set(race.id, history);
	}
	const points = race.map && race.map.complete && Array.isArray(race.map.points) ? race.map.points.filter(validMapPoint).slice(0, 512) : [];
	const hasGeometry = points.length > 0;
	// Once the course arrives, its fixed geometry owns the projection. A past
	// off-course/outlier position must not shrink the map for the entire race.
	if (hasGeometry) history.bounds = null;
	for (const racer of (Array.isArray(race.racers) ? race.racers : []).slice(0, 12)) {
		if (!racer || !Number.isInteger(racer.id) || racer.id < 0 || racer.id >= 12 || !racer.hasPosition || !validMapPoint(racer)) continue;
		if (!hasGeometry) points.push(racer);
		let trail = history.trails.get(racer.id);
		if (!trail) { trail = []; history.trails.set(racer.id, trail); }
		const last = trail[trail.length - 1];
		if (!last || last.x !== racer.x || last.z !== racer.z) {
			trail.push({ x: racer.x, z: racer.z });
			if (trail.length > 30) trail.shift();
		}
	}
	for (const point of points) {
		if (!history.bounds) history.bounds = { minX: point.x, maxX: point.x, minZ: point.z, maxZ: point.z };
		else {
			history.bounds.minX = Math.min(history.bounds.minX, point.x);
			history.bounds.maxX = Math.max(history.bounds.maxX, point.x);
			history.bounds.minZ = Math.min(history.bounds.minZ, point.z);
			history.bounds.maxZ = Math.max(history.bounds.maxZ, point.z);
		}
	}
}
function liveCourseMap(race, racers) {
	const history = state.live.maps.get(race.id);
	if (!history || !history.bounds) return '<div class="live-map-waiting"><svg class="icon" aria-hidden="true"><use href="#icon-rooms"/></svg><span>Waiting for map positions</span></div>';
	const bounds = history.bounds;
	const width = 640, height = 460, padding = 32;
	const spanX = Math.max(1000, bounds.maxX - bounds.minX), spanZ = Math.max(1000, bounds.maxZ - bounds.minZ);
	const scale = Math.min((width - padding * 2) / spanX, (height - padding * 2) / spanZ);
	const centerX = (bounds.minX + bounds.maxX) / 2, centerZ = (bounds.minZ + bounds.maxZ) / 2;
	const project = (point) => ({ x: width / 2 + (point.x - centerX) * scale, y: height / 2 + (point.z - centerZ) * scale });
	const pointText = (point) => { const p = project(point); return `${p.x.toFixed(2)},${p.y.toFixed(2)}`; };
	const points = race.map && race.map.complete && Array.isArray(race.map.points) ? race.map.points.slice(0, 512) : [];
	const segments = race.map && Array.isArray(race.map.segments) ? race.map.segments.slice(0, 1024) : [];
	const paths = segments.map((edge) => {
		if (!edge || !Number.isInteger(edge.from) || !Number.isInteger(edge.to) || !validMapPoint(points[edge.from]) || !validMapPoint(points[edge.to])) return "";
		return `M${pointText(points[edge.from])}L${pointText(points[edge.to])}`;
	}).join("");
	const trailLines = racers.map((racer) => {
		const trail = history.trails.get(racer.id) || [];
		return trail.length > 1 ? `<polyline points="${trail.map(pointText).join(" ")}" fill="none" stroke="${racerColor(racer)}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" opacity=".38"/>` : "";
	}).join("");
	const markers = [...racers].sort((a, b) => (b.position || 99) - (a.position || 99)).map((racer) => {
		if (!racer.hasPosition || !validMapPoint(racer)) return "";
		const p = project(racer);
		const inactive = racer.eliminated || racer.disconnected;
		return `<g transform="translate(${p.x.toFixed(2)} ${p.y.toFixed(2)})" opacity="${inactive ? ".5" : "1"}"><title>${esc(raceRacerName(racer))}${racer.position ? ` · place ${esc(racer.position)}` : ""}${inactive ? " · inactive" : ""}</title><circle r="12" fill="${racerColor(racer)}" stroke="#fff" stroke-width="2.5"/><text text-anchor="middle" dominant-baseline="central" fill="#fff" font-size="12" font-weight="700" font-family="Segoe UI, sans-serif">${raceMarkerNumber(racer)}</text></g>`;
	}).join("");
	const pathDots = !paths ? points.filter(validMapPoint).map((point) => { const p = project(point); return `<circle cx="${p.x.toFixed(2)}" cy="${p.y.toFixed(2)}" r="2" fill="#8eaeb7"/>`; }).join("") : "";
	return `<svg class="live-course-map" viewBox="0 0 ${width} ${height}" role="img" aria-label="${esc(courseName(race.courseId))}: live racer positions. Marker numbers match the leaderboard."><title>${esc(courseName(race.courseId))} live course map</title><desc>${paths ? "Course path and live racer positions." : "Reported racer positions and recent movement."} Marker numbers identify the racers in the leaderboard.</desc>${paths ? `<path d="${paths}" class="course-path-border"/><path d="${paths}" class="course-path"/>` : pathDots}${trailLines}${markers}</svg>`;
}
document.addEventListener("visibilitychange", () => {
	if (document.hidden) stopLiveRacePolling();
	else if (liveViewVisible() && liveAutoEnabled()) startLiveRacePolling();
	else if (liveViewVisible()) {
		paintLiveRaceAge();
		startLiveRaceAgeClock();
	}
});

/* ---------- Players ---------- */
async function renderPlayers() {
	try {
		const players = await apiGet("/api/players");
		const list = Array.isArray(players) ? players : [];
		const rows = list.map((p) => `
			<tr>
				<td>${esc(p.name) || "<span class='tag gray'>no name</span>"}${p.restricted ? ` <span class="tag red">restricted</span>` : ""}</td>
				<td class="mono">${fmtFC(p.fc)}</td>
				<td class="mono">${esc(p.pid)}</td>
				<td class="mono">${esc(p.ip)}</td>
				<td class="mono">${esc(p.device_id)}</td>
				<td>${esc(p.game)}</td>
				<td class="row-actions">
					<button class="btn btn-sm" data-act="profile" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}">Profile</button>
					<button class="btn btn-sm" data-act="kick" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}">Kick</button>
					<button class="btn btn-sm btn-danger" data-act="ban" data-pid="${esc(p.pid)}" data-name="${esc(p.name)}" data-ng="${esc(p.device_id)}">Ban</button>
				</td>
			</tr>`).join("");
		if (state.view !== "players") return;
		$("#content").innerHTML = `
			<div class="toolbar"><h2 style="margin:0">Players</h2><span class="tag gray">${list.length} connected</span></div>
			<div class="table-wrap"><table>
				<thead><tr><th>Name</th><th>Friend code</th><th>PID</th><th>IP</th><th>NG device</th><th>Game</th><th>Actions</th></tr></thead>
				<tbody>${rows || `<tr><td colspan="7" class="empty">No players connected.</td></tr>`}</tbody>
			</table></div>`;
	} catch (e) { onError(e); }
}

/* ---------- Bans ---------- */
async function renderBans() {
	try {
		const bans = await apiGet("/api/bans");
		const list = Array.isArray(bans) ? bans : [];
		const rows = list.map((b) => `
			<tr>
				<td>${esc(b.name) || "<span class='tag gray'>—</span>"} ${b.tos ? `<span class="tag red">TOS</span>` : `<span class="tag yellow">restrict</span>`}</td>
				<td class="mono">${fmtFC(b.fc)}</td>
				<td class="mono">${esc(b.pid)}</td>
				<td>${esc(b.reason) || "—"}</td>
				<td>${esc(b.moderator) || "—"}</td>
				<td title="${esc(fmtTime(b.expires))}">${esc(fmtRemaining(b.expires))}</td>
				<td class="row-actions"><button class="btn btn-sm" data-act="unban" data-pid="${esc(b.pid)}" data-name="${esc(b.name)}">Unban</button></td>
			</tr>`).join("");
		if (state.view !== "bans") return;
		$("#content").innerHTML = `
			<div class="toolbar">
				<h2 style="margin:0">Bans</h2><span class="tag gray">${list.length}</span>
				<span class="spacer"></span>
				<input class="search" id="ban-search" type="text" placeholder="Look up PID or NG id…">
				<button class="btn" data-act="baninfo">Search</button>
				<button class="btn" data-act="refresh-bans">↻</button>
				<button class="btn btn-danger" data-act="manual-ban">+ Ban by ID</button>
			</div>
			<div id="ban-search-result"></div>
			<div class="table-wrap"><table>
				<thead><tr><th>Player</th><th>Friend code</th><th>PID</th><th>Reason</th><th>By</th><th>Expires</th><th></th></tr></thead>
				<tbody>${rows || `<tr><td colspan="7" class="empty">No active bans.</td></tr>`}</tbody>
			</table></div>`;
	} catch (e) { onError(e); }
}

/* ---------- Settings ---------- */
async function renderSettings() {
	try {
		const [cfg, motd] = await Promise.all([apiGet("/api/config"), apiGet("/api/motd")]);
		state.config = cfg;
		const sel = (v, opt) => (v === opt ? "selected" : "");
		const cpuAvailable = cfg.onlineCPUAvailable === true;
		const knockoutAvailable = cfg.knockoutAvailable === true;
		if (state.view !== "settings") return;
		$("#content").innerHTML = `
			<h2 style="margin:0 0 16px">Settings</h2>

			<div class="section-title">Panel preferences</div>
			<div class="card"><form class="stack" id="preferences-form">
				<div class="field">
					<label for="pf-refresh">Automatic refresh</label>
					<select id="pf-refresh" ${state.preferencesLoaded ? "" : "disabled"}>
						${[0, 3, 5, 10, 30, 60].map((n) => `<option value="${n}" ${sel(state.preferences.refreshSeconds, n)}>${n === 0 ? "Manual only" : `Every ${n} seconds`}</option>`).join("")}
					</select>
					<span class="hint">The refresh interval and your last opened tab are saved in SQLite and shared across Host Manager browsers. In manual mode, open a tab to refresh it. ${state.preferencesLoaded ? "" : "Preferences are unavailable; reload the page to try again."}</span>
				</div>
				<div class="form-actions"><button class="btn btn-primary" type="submit" ${state.preferencesLoaded ? "" : "disabled"}>Save panel preferences</button></div>
			</form></div>

			<div class="section-title">Message of the day</div>
			<div class="card"><form class="stack" id="motd-form">
				<div class="field">
					<textarea id="motd-text" placeholder="Shown to players. Leave blank for none.">${esc(motd.text || "")}</textarea>
					<span class="hint">Applies immediately — no restart needed.</span>
				</div>
				<div class="form-actions"><button class="btn btn-primary" type="submit">Save MOTD</button></div>
			</form></div>

			<div class="section-title">Admin password</div>
			<div class="card"><form class="stack" id="password-form">
				<div class="field"><label for="pw-current">Current password</label><input type="password" id="pw-current" autocomplete="current-password" placeholder="Required"></div>
				<div class="field"><label for="pw-new">New password</label><input type="password" id="pw-new" autocomplete="new-password" placeholder="At least 8 characters"></div>
				<span class="hint">Stored hashed in the SQLite database. Changing it does not log out existing sessions.</span>
				<div class="form-actions"><button class="btn btn-primary" type="submit">Change password</button></div>
			</form></div>

			<div class="section-title">WFC server</div>
			<div class="card"><form class="stack" id="config-form">
				<div class="field">
					<label for="cf-name">Server name</label>
					<input type="text" id="cf-name" value="${esc(cfg.serverName || "")}">
				</div>
				<div class="field">
					<label for="cf-multi">Multiple consoles per account</label>
					<select id="cf-multi">
						<option value="never" ${sel(cfg.allowMultipleDeviceIDs, "never")}>Never (strict — one console per account)</option>
						<option value="SameIPAddress" ${sel(cfg.allowMultipleDeviceIDs, "SameIPAddress")}>Same IP only</option>
						<option value="always" ${sel(cfg.allowMultipleDeviceIDs, "always")}>Always allow</option>
					</select>
					<span class="hint">Applies live.</span>
				</div>
				<div class="field switch"><input type="checkbox" id="cf-dolphin" ${cfg.allowDefaultDolphinKeys ? "checked" : ""}><label for="cf-dolphin" style="margin:0">Allow default Dolphin keys <span class="hint">(needed for Dolphin test clients) — applies live</span></label></div>
				<div class="field switch"><input type="checkbox" id="cf-nodev" ${cfg.allowConnectWithoutDeviceID ? "checked" : ""}><label for="cf-nodev" style="margin:0">Allow connecting without a device ID <span class="hint">— applies live</span></label></div>
				<div class="field switch"><input type="checkbox" id="cf-expwii" ${cfg.enableHttpsExploitWii ? "checked" : ""}><label for="cf-expwii" style="margin:0">Wii HTTPS exploit <span class="hint">— saved; needs restart</span></label></div>
				<div class="field switch"><input type="checkbox" id="cf-expds" ${cfg.enableHttpsExploitDS ? "checked" : ""}><label for="cf-expds" style="margin:0">DS HTTPS exploit <span class="hint">— saved; needs restart</span></label></div>
				<div class="field">
					<label for="cf-log">Log verbosity</label>
					<select id="cf-log">
						${[0,1,2,3,4].map((n) => `<option value="${n}" ${sel(cfg.logLevel, n)}>${n} — ${["none","general","+errors","+warnings","+info"][n]}</option>`).join("")}
					</select>
					<span class="hint">Applies live.</span>
				</div>
				<fieldset class="cpu-settings">
					<legend>Race connections</legend>
					<div class="field switch"><input type="checkbox" id="cf-disable-inactivity" ${cfg.disableInactivityDisconnect !== false ? "checked" : ""} aria-describedby="cf-disable-inactivity-help"><label for="cf-disable-inactivity" style="margin:0">Disable inactivity disconnects</label></div>
					<span class="hint" id="cf-disable-inactivity-help">On by default. Updated PAL online VS players stay connected when their input is unchanged or their kart stops moving. Controller loss, network failures and normal race finishing still apply. Saved changes apply to connected compatible clients.</span>
				</fieldset>
				<fieldset class="cpu-settings">
					<legend>Knockout <span class="tag ${knockoutAvailable ? "warn" : ""}">${knockoutAvailable ? "Experimental" : "Unavailable"}</span></legend>
					<div class="field switch"><input type="checkbox" id="cf-knockout" ${cfg.knockoutEnabled ? "checked" : ""} ${knockoutAvailable ? "" : "disabled"} aria-describedby="cf-knockout-help"><label for="cf-knockout" style="margin:0">Enable Knockout in private rooms</label></div>
					<span class="hint" id="cf-knockout-help">Applies to the next agreed private VS race, including CPU racers. The public Knockdown option always runs Knockout in its own matchmaking pool; Worldwide runs normal VS. Requires compatible PAL disc payloads and one local human per console. Multiplayer validation is pending.</span>
					<div class="field">
						<label for="cf-knockout-mode">Knockout mode</label>
						<select id="cf-knockout-mode" ${knockoutAvailable ? "" : "disabled"} aria-describedby="cf-knockout-mode-help">
							<option value="round" ${sel(cfg.knockoutMode ?? "round", "round")}>Round Knockdown</option>
								<option value="race" ${sel(cfg.knockoutMode ?? "round", "race")}>Race Knockout</option>
						</select>
					</div>
					<div class="field">
						<label for="cf-knockout-count">Race Knockout eliminations</label>
						<select id="cf-knockout-count" ${knockoutAvailable ? "" : "disabled"} aria-describedby="cf-knockout-mode-help">
							<option value="1" ${sel(cfg.knockoutEliminationCount ?? 1, 1)}>1 racer</option>
								<option value="2" ${sel(cfg.knockoutEliminationCount ?? 1, 2)}>2 racers</option>
						</select>
						<span class="hint" id="cf-knockout-mode-help">Round Knockdown eliminates the last active racer at each leader lap. Race Knockout eliminates 1 or 2 after each completed race, keeping two for the final. Eliminated and rejoining players spectate until the winner; newcomers spectate too. The host closes the room after the series before another can begin. Applies to the public Knockdown pool and enabled private rooms.</span>
					</div>
				</fieldset>
				<fieldset class="cpu-settings">
					<legend>Online CPUs <span class="tag ${cpuAvailable ? "warn" : ""}">${cpuAvailable ? "Experimental" : "In development"}</span></legend>
					<div class="field switch"><input type="checkbox" id="cf-online-cpu" ${cfg.onlineCPUEnabled ? "checked" : ""} ${cpuAvailable ? "" : "disabled"} aria-describedby="cf-online-cpu-help"><label for="cf-online-cpu" style="margin:0">Enable CPU racers when available</label></div>
					<div class="field switch"><input type="checkbox" id="cf-online-cpu-auto-fill" ${cfg.onlineCPUAutoFill ? "checked" : ""} ${cpuAvailable && cfg.onlineCPUEnabled ? "" : "disabled"} aria-describedby="cf-online-cpu-fill-help"><label for="cf-online-cpu-auto-fill" style="margin:0">Auto-fill empty slots to 12 racers</label></div>
					<span class="hint" id="cf-online-cpu-fill-help">Real players take priority between ordinary VS races. For example, 4 real players + 8 CPUs becomes 5 real players + 7 CPUs next race. No mid-race takeover. Race Knockout retains its series identities and elimination status; joining does not restart the series or revive CPUs.</span>
					<div class="field">
						<label for="cf-online-cpu-count">CPUs to add when auto-fill is off</label>
						<input type="number" id="cf-online-cpu-count" min="1" max="11" step="1" value="${esc(cfg.onlineCPUCount ?? 4)}" ${cpuAvailable && cfg.onlineCPUEnabled && !cfg.onlineCPUAutoFill ? "" : "disabled"} required aria-describedby="cf-online-cpu-help">
					</div>
					<span class="hint" id="cf-online-cpu-help">${cpuAvailable ? "Experimental PAL online VS support for public matchmaking and private rooms. Adds CPUs up to 12 total racers from the next race. All players need the current compatible PAL disc payload. CPU item synchronization is experimental; Battle remains unsupported. Multiplayer validation is pending." : "CPU support for public matchmaking and private rooms is in development for this server build. Your preference and count are saved for a compatible build; CPUs are currently unavailable."}</span>
				</fieldset>
				<div class="form-actions"><button class="btn btn-primary" type="submit">Save settings</button></div>
			</form></div>

			<div class="section-title">Mario Kart Wii</div>
			<div class="note">Live match state is visible in <b>Rooms</b> (current course, engine class, race number, region); you can edit accounts and set a <b>per-player message</b> from <b>Players → Profile</b> (delivered as that player's MOTD on their next connect — server-only, no payload change). Gameplay the game itself owns — forcing a track, changing item behaviour, or a live mid-race popup — needs new client-payload hooks. Those run on unmodified consoles (the payload is injected at connect, no homebrew), but require building the payload with devkitPPC (PLAN Phase 4).</div>

			<div class="section-title">Network (read-only — edit config.xml and restart)</div>
			<div class="card"><div class="info-grid">
				${infoRow("Admin panel", cfg.adminAddress)}
				${infoRow("GameSpy bind", cfg.gsAddress)}
				${infoRow("NAS HTTP", cfg.nasAddress + ":" + cfg.nasPort)}
				${infoRow("NAS HTTPS", cfg.nasPortHttps + (cfg.enableHttps ? " (on)" : " (off)"))}
				${infoRow("Payload server", cfg.payloadServerAddress || "integrated")}
				${infoRow("Database (SQLite)", cfg.databasePath)}
			</div></div>`;

		$("#preferences-form").addEventListener("submit", async (e) => {
			e.preventDefault();
			if (!state.preferencesLoaded) return;
			const refreshSeconds = Number($("#pf-refresh").value);
			if (!REFRESH_INTERVALS.has(refreshSeconds)) return;
			try {
				await savePanelPreferences({ refreshSeconds });
				state.preferences.refreshSeconds = refreshSeconds;
				state.live.auto = null;
				startPolling();
				toast("Panel preferences saved", "good");
			} catch (err) { onError(err); }
		});
		$("#motd-form").addEventListener("submit", async (e) => {
			e.preventDefault();
			try { await apiPost("/api/motd", { text: $("#motd-text").value }); toast("MOTD saved", "good"); }
			catch (err) { onError(err); }
		});
		$("#password-form").addEventListener("submit", async (e) => {
			e.preventDefault();
			const newPw = $("#pw-new").value;
			if (newPw.length < 8) { toast("New password must be at least 8 characters", "bad"); return; }
			try {
				await apiPost("/api/password", { current_password: $("#pw-current").value, new_password: newPw });
				toast("Password changed", "good");
				$("#pw-current").value = ""; $("#pw-new").value = "";
			} catch (err) {
				if (err.data && err.data.error === "invalid_password") { toast("Current password is wrong, or new password too short", "bad"); return; }
				onError(err);
			}
		});
		const syncCPUControls = () => {
			const enabled = cpuAvailable && $("#cf-online-cpu").checked;
			$("#cf-online-cpu-auto-fill").disabled = !enabled;
			$("#cf-online-cpu-count").disabled = !enabled || $("#cf-online-cpu-auto-fill").checked;
		};
		$("#cf-online-cpu").addEventListener("change", syncCPUControls);
		$("#cf-online-cpu-auto-fill").addEventListener("change", syncCPUControls);
		const syncKnockoutControls = () => {
			$("#cf-knockout-count").disabled = !knockoutAvailable || $("#cf-knockout-mode").value !== "race";
		};
		$("#cf-knockout-mode").addEventListener("change", syncKnockoutControls);
		syncKnockoutControls();
		$("#config-form").addEventListener("submit", async (e) => {
			e.preventDefault();
			const onlineCPUAutoFill = $("#cf-online-cpu-auto-fill").checked;
			const onlineCPUCount = Number($("#cf-online-cpu-count").value);
			const knockoutMode = $("#cf-knockout-mode").value;
			const knockoutEliminationCount = Number($("#cf-knockout-count").value);
			if (!onlineCPUAutoFill && (!Number.isInteger(onlineCPUCount) || onlineCPUCount < 1 || onlineCPUCount > 11)) {
				toast("CPU count must be a whole number from 1 to 11", "bad");
				return;
			}
			if (!["round", "race"].includes(knockoutMode) || ![1, 2].includes(knockoutEliminationCount)) {
				toast("Choose a Knockout mode and 1 or 2 racers", "bad");
				return;
			}
			try {
				await apiPost("/api/config", {
					serverName: $("#cf-name").value,
					allowMultipleDeviceIDs: $("#cf-multi").value,
					allowDefaultDolphinKeys: $("#cf-dolphin").checked,
					allowConnectWithoutDeviceID: $("#cf-nodev").checked,
					enableHttpsExploitWii: $("#cf-expwii").checked,
					enableHttpsExploitDS: $("#cf-expds").checked,
					logLevel: parseInt($("#cf-log").value, 10),
					disableInactivityDisconnect: $("#cf-disable-inactivity").checked,
					onlineCPUEnabled: $("#cf-online-cpu").checked,
					onlineCPUAutoFill,
					knockoutEnabled: $("#cf-knockout").checked,
					knockoutMode,
					knockoutEliminationCount,
					...(onlineCPUAutoFill ? {} : { onlineCPUCount }),
				});
				toast("Settings saved to config.xml", "good");
				$("#server-name").textContent = $("#cf-name").value || "mkwfc";
			} catch (err) { onError(err); }
		});
	} catch (e) { onError(e); }
}

/* ---------- Actions (event delegation) ---------- */
$("#content").addEventListener("click", async (e) => {
	const btn = e.target.closest("[data-act]");
	if (!btn) return;
	const { act, pid, name, ng } = btn.dataset;

	if (act === "profile") return profileModal(pid);
	if (act === "kick") return kickModal(pid, name);
	if (act === "ban") return banModal(pid, name, ng);
	if (act === "manual-ban") return banModal("", "", "");
	if (act === "refresh-bans") return renderBans();

	if (act === "unban") {
		if (!confirm(`Unban ${name || "PID " + pid}?`)) return;
		try { await apiPost("/api/unban", { pid: Number(pid) }); toast("Unbanned", "good"); renderBans(); }
		catch (err) { onError(err); }
	}

	if (act === "baninfo") {
		const q = $("#ban-search").value.trim();
		if (!q) return;
		const box = $("#ban-search-result");
		try {
			const r = await apiGet("/api/baninfo", { q });
			if (r && r.error) { box.innerHTML = `<div class="note">No active ban found for <code>${esc(q)}</code>.</div>`; return; }
			box.innerHTML = `<div class="card" style="margin-bottom:14px">
				<div class="info-row"><span class="k">Name</span><span class="v">${esc(r.name) || "—"}</span></div>
				<div class="info-row"><span class="k">PID / FC</span><span class="v">${esc(r.pid)} · ${fmtFC(r.fc)}</span></div>
				<div class="info-row"><span class="k">Type</span><span class="v">${r.tos ? "TOS (full)" : "restriction"}</span></div>
				<div class="info-row"><span class="k">Reason</span><span class="v">${esc(r.reason) || "—"}</span></div>
				<div class="info-row"><span class="k">Expires</span><span class="v">${esc(fmtTime(r.expires))} (${esc(fmtRemaining(r.expires))})</span></div>
				<div class="form-actions" style="margin-top:12px"><button class="btn btn-sm" data-act="unban" data-pid="${esc(r.pid)}" data-name="${esc(r.name)}">Unban</button></div>
			</div>`;
		} catch (err) { onError(err); }
	}
});

/* ---------- Modals ---------- */
function openModal(html) {
	const root = $("#modal-root");
	root.innerHTML = `<div class="modal-backdrop">${html}</div>`;
	root.querySelector(".modal-backdrop").addEventListener("click", (e) => {
		if (e.target.classList.contains("modal-backdrop")) closeModal();
	});
	return root.querySelector(".modal");
}
function closeModal() { $("#modal-root").innerHTML = ""; }

function kickModal(pid, name) {
	const m = openModal(`
		<div class="modal">
			<h3>Kick player</h3>
			<p class="modal-sub">${esc(name) || "PID " + esc(pid)} · PID ${esc(pid)}</p>
			<form class="stack" id="kick-form">
				<div class="field"><label>Reason (shown to the player)</label><input type="text" id="kick-reason" value="Kicked by host" required></div>
				<div class="modal-actions"><button type="button" class="btn" data-close>Cancel</button><button type="submit" class="btn btn-danger">Kick</button></div>
			</form>
		</div>`);
	m.querySelector("[data-close]").addEventListener("click", closeModal);
	m.querySelector("#kick-form").addEventListener("submit", async (e) => {
		e.preventDefault();
		try {
			await apiPost("/api/kick", { pid: Number(pid), reason: $("#kick-reason").value });
			toast("Kicked " + (name || "PID " + pid), "good");
			closeModal();
			if (POLLED.has(state.view)) render();
		} catch (err) { onError(err); }
	});
}

async function profileModal(pid) {
	let p;
	try { p = await apiGet("/api/profile", { pid }); }
	catch (err) { return onError(err); }
	const isMKW = /^RMC[PEJK]$/.test(p.game_code || "");
	let penalty;
	if (isMKW) {
		try { penalty = await apiGet("/api/penalty", { pid }); }
		catch (err) { return onError(err); }
	}
	const m = openModal(`
		<div class="modal">
			<h3>Edit profile</h3>
			<p class="modal-sub">${esc(p.name) || "PID " + esc(p.pid)} · PID ${esc(p.pid)} ${p.online ? `<span class="tag green">online</span>` : `<span class="tag gray">offline</span>`}${p.restricted ? ` <span class="tag red">restricted</span>` : ""}</p>
			<form class="stack" id="profile-form">
				<div class="field"><label>In-game first name</label><input type="text" id="pf-first" value="${esc(p.firstname)}" maxlength="15"></div>
				<div class="field"><label>In-game last name</label><input type="text" id="pf-last" value="${esc(p.lastname)}" maxlength="15"></div>
				<div class="field switch"><input type="checkbox" id="pf-oh" ${p.open_host ? "checked" : ""}><label for="pf-oh" style="margin:0">Open host <span class="hint">— let anyone join this player's rooms without being friends</span></label></div>
				<div class="field">
					<label for="pf-msg">Message to this player</label>
					<textarea id="pf-msg" maxlength="512" placeholder="Shown to only this player on the Nintendo WFC menu when they next connect. Blank = none.">${esc(p.message || "")}</textarea>
					<span class="hint">Delivered as this player's MOTD (wl:motd) — appears at connect / on the WFC menu, not live mid-race. Takes effect next time they connect.</span>
				</div>
				${isMKW ? `<div class="card" style="margin:0">
					<h4>Matchmaking penalty</h4>
					<p id="pf-penalty-status">${esc(penaltyResetText(penalty))}</p>
					<p class="hint">Clear this license's disconnect penalty while keeping progress, ratings and friend code. Finish the current race and return to the Nintendo WFC main menu to apply it. Restart public searching after it is cleared.</p>
					<button type="button" class="btn btn-sm" id="pf-penalty">Remove penalty</button>
					<button type="button" class="btn btn-sm" id="pf-penalty-refresh">Refresh status</button>
				</div>` : ""}
				<div class="card" style="margin:0"><div class="info-grid">
					${infoRow("Friend code", fmtFC(p.fc))}
					${infoRow("Game", p.game_code || "—")}
					${infoRow("Email (login)", p.email || "—")}
					${infoRow("GameSpy nick", p.unique_nick || "—")}
					${infoRow("Last IP", p.last_ip || "—")}
				</div></div>
				<div class="note">Name and open-host are saved to the account database and apply to the live session when the player is online. Email/nick/friend code are account identity and read-only here.</div>
				<div class="modal-actions"><button type="button" class="btn" data-close>Cancel</button><button type="submit" class="btn btn-primary">Save profile</button></div>
			</form>
		</div>`);
	m.querySelector("[data-close]").addEventListener("click", closeModal);
	if (isMKW) {
		const status = m.querySelector("#pf-penalty-status");
		const reset = m.querySelector("#pf-penalty");
		reset.addEventListener("click", async () => {
			reset.disabled = true;
			try {
				penalty = await apiPost("/api/penalty", { pid: Number(pid) });
				status.textContent = penaltyResetText(penalty);
				toast(penalty.state === "cleared" ? "Penalty cleared" : "Penalty removal queued", "good");
			} catch (err) { onError(err); }
			finally { reset.disabled = false; }
		});
		m.querySelector("#pf-penalty-refresh").addEventListener("click", async () => {
			try {
				penalty = await apiGet("/api/penalty", { pid });
				status.textContent = penaltyResetText(penalty);
			} catch (err) { onError(err); }
		});
	}
	m.querySelector("#profile-form").addEventListener("submit", async (e) => {
		e.preventDefault();
		try {
			await apiPost("/api/profile", {
				pid: Number(pid),
				firstname: $("#pf-first").value,
				lastname: $("#pf-last").value,
				open_host: $("#pf-oh").checked,
				message: $("#pf-msg").value,
			});
			toast("Profile saved", "good");
			closeModal();
			if (POLLED.has(state.view)) render();
		} catch (err) { onError(err); }
	});
}

function penaltyResetText(info) {
	if (info.state === "cleared") return "Penalty cleared and saved by the game. Restart public searching to use it.";
	if (info.state === "queued") return info.supported
		? "Queued. Return to the Nintendo WFC main menu, then refresh this status."
		: "Queued for the next connection using the current PAL game payload.";
	if (info.state === "failed") return info.result === 1
		? "Could not match this request to the selected game license. Reconnect that license and try again."
		: "The game could not confirm that the cleared penalty was saved. Return to the WFC main menu and try again.";
	return "No penalty reset requested. Removal requires the current PAL game payload.";
}

function banModal(pid, name, ng) {
	const manual = !pid;
	const m = openModal(`
		<div class="modal">
			<h3>${manual ? "Ban by profile ID" : "Ban player"}</h3>
			<p class="modal-sub">${manual ? "Enter the profile ID (PID) to ban." : esc(name) || "PID " + esc(pid)}${ng ? " · NG " + esc(ng) : ""}</p>
			<form class="stack" id="ban-form">
				${manual ? `<div class="field"><label>Profile ID</label><input type="number" id="ban-pid" placeholder="e.g. 1000000001" required></div>` : ""}
				<div class="field"><label>Duration</label>
					<div class="form-row">
						<input type="number" id="ban-days" min="0" value="7" placeholder="days">
						<input type="number" id="ban-hours" min="0" value="0" placeholder="hours">
						<input type="number" id="ban-mins" min="0" value="0" placeholder="minutes">
					</div>
					<span class="hint">days / hours / minutes — must total more than 0.</span>
				</div>
				<div class="field"><label>Reason (shown to the player)</label><input type="text" id="ban-reason" value="Banned by host" required></div>
				<div class="field"><label>Internal note (optional, not shown)</label><input type="text" id="ban-hidden" placeholder="e.g. cheating in room ABCD"></div>
				<div class="field switch"><input type="checkbox" id="ban-tos"><label for="ban-tos" style="margin:0">Full ban (TOS) — harsher than a restriction</label></div>
				<div class="note">The ban is recorded against the account and its NG device ID, so on a real Wii it blocks the whole console. On Dolphin with shared default keys it applies to the profile only.</div>
				<div class="modal-actions"><button type="button" class="btn" data-close>Cancel</button><button type="submit" class="btn btn-danger">Ban</button></div>
			</form>
		</div>`);
	m.querySelector("[data-close]").addEventListener("click", closeModal);
	m.querySelector("#ban-form").addEventListener("submit", async (e) => {
		e.preventDefault();
		const realPid = manual ? Number($("#ban-pid").value) : Number(pid);
		if (!realPid) { toast("Enter a valid PID", "bad"); return; }
		try {
			await apiPost("/api/ban", {
				pid: realPid,
				days: Number($("#ban-days").value) || 0,
				hours: Number($("#ban-hours").value) || 0,
				minutes: Number($("#ban-mins").value) || 0,
				reason: $("#ban-reason").value,
				reason_hidden: $("#ban-hidden").value,
				tos: $("#ban-tos").checked,
				moderator: "host-panel",
			});
			toast("Banned " + (name || "PID " + realPid), "good");
			closeModal();
			render();
		} catch (err) { onError(err); }
	});
}

/* ---------- Boot ---------- */
(async function boot() {
	$("#gate-desc").textContent = "Enter your admin password.";
	$("#gate-secret").placeholder = "Admin password";
	$("#gate-submit").textContent = "Unlock";
	if (state.secret) {
		try { await enterApp(); return; }
		catch { state.secret = ""; }
	}
	showGate();
})();
