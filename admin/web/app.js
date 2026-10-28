"use strict";

/* ---------- State & helpers ---------- */
const SECRET_KEY = "wfc_admin_secret";
const state = {
	secret: localStorage.getItem(SECRET_KEY) || "", // holds a session token once logged in
	view: "dashboard",
	config: null,
	timer: null,
	gateMode: "login",
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
async function apiGet(path, params = {}) {
	const u = new URL(path, location.origin);
	u.searchParams.set("secret", state.secret);
	for (const [k, v] of Object.entries(params)) u.searchParams.set(k, v);
	return handle(await fetch(u, { headers: { Accept: "application/json" } }));
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
		showGate("Secret rejected. Try again.");
		return;
	}
	toast((e && e.message) || "Request failed", "bad");
}

/* ---------- Gate ---------- */
function showGate(err = "") {
	$("#app").classList.add("hidden");
	$("#gate").classList.remove("hidden");
	$("#gate-error").textContent = err;
	$("#gate-secret").value = "";
	$("#gate-newpass").value = "";
	$("#gate-secret").focus();
}
function showApp() {
	$("#gate").classList.add("hidden");
	$("#app").classList.remove("hidden");
}
// The gate has two modes: "login" (enter admin password) and "setup" (first
// run — enter the config.xml apiSecret to authorize, then choose a password).
function setGateMode(mode) {
	state.gateMode = mode;
	const setup = mode === "setup";
	$("#gate-desc").textContent = setup
		? "First-time setup: enter the apiSecret from config.xml to authorize, then choose an admin password."
		: "Enter your admin password.";
	$("#gate-secret").placeholder = setup ? "apiSecret (from config.xml)" : "Admin password";
	$("#gate-newpass").classList.toggle("hidden", !setup);
	$("#gate-submit").textContent = setup ? "Set password" : "Unlock";
}
async function enterApp() {
	state.config = await apiGet("/api/config");
	showApp();
	setView(state.view);
	startPolling();
}
$("#gate-form").addEventListener("submit", async (e) => {
	e.preventDefault();
	const field = $("#gate-secret").value.trim();
	if (!field) return;
	try {
		let resp;
		if (state.gateMode === "setup") {
			const pw = $("#gate-newpass").value;
			if (pw.length < 8) { $("#gate-error").textContent = "Password must be at least 8 characters."; return; }
			resp = await apiPost("/api/setup", { secret: field, password: pw });
		} else {
			resp = await apiPost("/api/login", { password: field });
		}
		state.secret = resp.token;
		if ($("#gate-remember").checked) localStorage.setItem(SECRET_KEY, state.secret);
		else localStorage.removeItem(SECRET_KEY);
		await enterApp();
	} catch (err) {
		state.secret = "";
		if (err.data && err.data.error === "setup_required") { setGateMode("login"); $("#gate-error").textContent = "Enter your admin password."; return; }
		if (err.data && err.data.error === "setup_already_done") { setGateMode("login"); $("#gate-error").textContent = "A password is already set — log in."; return; }
		$("#gate-error").textContent = err.auth ? (state.gateMode === "setup" ? "Wrong apiSecret." : "Wrong password.") : "Could not reach server.";
	}
});
$("#btn-logout").addEventListener("click", async () => {
	try { await apiPost("/api/logout", {}); } catch { /* best effort */ }
	localStorage.removeItem(SECRET_KEY);
	state.secret = "";
	stopPolling();
	setGateMode("login");
	showGate("Locked.");
});

/* ---------- Tabs ---------- */
$("#tabs").addEventListener("click", (e) => {
	const btn = e.target.closest(".tab");
	if (!btn) return;
	setView(btn.dataset.view);
});
function setView(view) {
	state.view = view;
	for (const t of document.querySelectorAll(".tab"))
		t.classList.toggle("active", t.dataset.view === view);
	render();
}

/* ---------- Polling ---------- */
const POLLED = new Set(["dashboard", "rooms", "players"]);
function startPolling() {
	stopPolling();
	refreshStats();
	state.timer = setInterval(() => {
		refreshStats();
		if (POLLED.has(state.view)) render();
	}, 3000);
}
function stopPolling() {
	if (state.timer) clearInterval(state.timer);
	state.timer = null;
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
		if (e.auth) { stopPolling(); showGate("Secret rejected."); }
	}
}

/* ---------- Render dispatch ---------- */
function render() {
	switch (state.view) {
		case "dashboard": return renderDashboard();
		case "rooms": return renderRooms();
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
			<div class="grid">
				<div class="card stat-card"><div class="stat-num accent">${g.online || 0}</div><div class="stat-label">Players online</div></div>
				<div class="card stat-card"><div class="stat-num blue">${g.active || 0}</div><div class="stat-label">In a race / match</div></div>
				<div class="card stat-card"><div class="stat-num">${g.groups || 0}</div><div class="stat-label">Active rooms</div></div>
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
	return `<div class="room">
		<div class="room-head">
			<span class="rid">${esc(gr.id)}</span>
			<span class="tag ${typeTag}">${esc(gr.type || "?")}</span>
			<span class="tag blue">${esc(gr.game)}</span>
			${gr.rk ? `<span class="tag gray">${esc(gr.rk)}</span>` : ""}
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
		if (state.view !== "settings") return;
		$("#content").innerHTML = `
			<h2 style="margin:0 0 16px">Settings</h2>

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
		$("#config-form").addEventListener("submit", async (e) => {
			e.preventDefault();
			try {
				await apiPost("/api/config", {
					serverName: $("#cf-name").value,
					allowMultipleDeviceIDs: $("#cf-multi").value,
					allowDefaultDolphinKeys: $("#cf-dolphin").checked,
					allowConnectWithoutDeviceID: $("#cf-nodev").checked,
					enableHttpsExploitWii: $("#cf-expwii").checked,
					enableHttpsExploitDS: $("#cf-expds").checked,
					logLevel: parseInt($("#cf-log").value, 10),
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
	let status;
	try { status = await apiGet("/api/auth/status"); }
	catch { status = { password_set: true }; }
	setGateMode(status.password_set ? "login" : "setup");
	if (status.password_set && state.secret) {
		try { await enterApp(); return; } // stored token still valid
		catch { state.secret = ""; }
	}
	showGate();
})();
