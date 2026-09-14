const loggedOutSection = document.getElementById("loggedOutSection");
const loggedInSection = document.getElementById("loggedInSection");
const welcomeText = document.getElementById("welcomeText");
const createRoomBtn = document.getElementById("createRoomBtn");
const logoutBtn = document.getElementById("logoutBtn");
const guestName = document.getElementById("guestName");
const roomCodeInput = document.getElementById("roomCodeInput");
const joinAsGuestBtn = document.getElementById("joinAsGuestBtn");
const joinRoomInput = document.getElementById("joinRoomInput");
const joinAsUserBtn = document.getElementById("joinAsUserBtn");

let currentUserName = null;

async function checkLoginStatus() {
    try {
        const res = await fetch("/api/me");
        const data = await res.json();
        if (data.loggedIn) {
            loggedOutSection.style.display = "none";
            loggedInSection.style.display = "block";
            welcomeText.textContent = `Hoş geldin, ${data.name}`;
            currentUserName = data.name;
        } else {
            loggedOutSection.style.display = "block";
            loggedInSection.style.display = "none";
        }
    } catch (err) {
        console.error("Oturum kontrolü başarısız:", err);
    }
}

checkLoginStatus();

createRoomBtn.addEventListener("click", async () => {
    try {
        const res = await fetch("/api/create-room");
        const data = await res.json();
        if (data.room) {
            const name = encodeURIComponent(data.hostName || "Host");
            window.location.href = `/call.html?room=${data.room}&name=${name}`;
        } else {
            alert(data.error || "Oda kurulamadı.");
        }
    } catch (err) {
        console.error("Oda kurulamadı:", err);
        alert("Oda kurulamadı, lütfen tekrar deneyin.");
    }
});

logoutBtn.addEventListener("click", () => {
    window.location.href = "/auth/logout";
});

function extractRoomId(input) {
    try {
        const url = new URL(input);
        const roomParam = url.searchParams.get("room");
        if (roomParam) return roomParam;
    } catch (e) {
    }
    return input.trim();
}

joinAsUserBtn.addEventListener("click", () => {
    const roomRaw = joinRoomInput.value.trim();

    if (!roomRaw) {
        alert("Lütfen bir oda kodu ya da link girin.");
        return;
    }

    const roomId = extractRoomId(roomRaw);
    const name = encodeURIComponent(currentUserName || "Kullanıcı");
    window.location.href = `/call.html?room=${roomId}&name=${name}`;
});