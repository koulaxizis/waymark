// Greek and English strings. Greek is the default.
const S = {
  el: {
    settings: "Ρυθμίσεις", disconnectAll: "Αποσύνδεση όλων", updateGet: "Λήψη", later: "Αργότερα",
    updateAvail: "Νέα σταθερή έκδοση {0} διαθέσιμη.", upToDate: "Έχεις την πιο πρόσφατη έκδοση.", noInternet: "Δεν υπάρχει σύνδεση στο ίντερνετ.",
    setupTitle: "Τι είναι αυτός ο υπολογιστής;", setupLead: "Το επιλέγεις μία φορά. Αλλάζει από τις Ρυθμίσεις.",
    modeAgent: "Agent", modeAgentText: "Ο υπολογιστής του ομιλητή. Δέχεται αρχεία και παίζει την παρουσίαση.",
    modeController: "Controller", modeControllerText: "Ο υπολογιστής του χειριστή, της γραμματείας ή της εποπτείας.",
    roleLead: "Ρόλος:", role_operator: "Χειριστής", role_secretariat: "Γραμματεία", role_supervisor: "Εποπτεία",
    nameLabel: "Όνομα που βλέπουν οι άλλοι", start: "Έναρξη",
    connect: "Σύνδεση", connectLead: "Γράψε τον κωδικό που δείχνει ο Controller.", connectBtn: "Σύνδεση",
    connecting: "Σύνδεση…", connectedOk: "Συνδέθηκε.", connected: "Συνδεδεμένοι", nobody: "Κανείς ακόμη.",
    disconnect: "Αποσύνδεση", hasControl: "έχει τον έλεγχο", folder: "Φάκελος αρχείων", openFolder: "Άνοιγμα φακέλου",
    changeFolder: "Αλλαγή φακέλου", folderNext: "Ο νέος φάκελος ισχύει από την επόμενη εκκίνηση.",
    presentation: "Παρουσίαση", files: "Αρχεία", log: "Καταγραφή", noFile: "Κανένα ανοιχτό αρχείο", emptyFolder: "Ο φάκελος είναι άδειος.",
    statusConnected: "Συνδεδεμένο ({0})", statusWaiting: "Αναμονή σύνδεσης",
    st_idle: "Αδρανές", st_open: "Έτοιμο", st_showing: "Σε προβολή", st_black: "Μαύρη οθόνη",
    app_powerpoint: "PowerPoint", app_impress: "LibreOffice Impress", app_viewer: "Ενσωματωμένος PDF viewer", app_system: "Εφαρμογή του συστήματος",
    errCodeFormat: "Ο κωδικός έχει 12 ή 16 χαρακτήρες.", errReach: "Δεν βρέθηκε ο Controller. Είστε στο ίδιο δίκτυο; Δοκίμασε τον μεγάλο κωδικό ή καλώδιο.",
    errWrongCode: "Λάθος κωδικός. Έλεγξε τον κωδικό στην οθόνη του Controller.", errRejected: "Η σύνδεση δεν έγινε δεκτή.",
    errVersion: "Ο Agent και ο Controller χρειάζονται την ίδια έκδοση. Ενημέρωσε και τα δύο.",
    promptTitle: "Αίτημα σύνδεσης", promptWho: "{0} ζητά σύνδεση ως {1}.", promptHint: "Ο αριθμός πρέπει να είναι ίδιος με την οθόνη του Controller.",
    promptRole: "Δικαιώματα", accept: "Αποδοχή", reject: "Άρνηση",
    yourCode: "Ο κωδικός σου", yourCodeLead: "Ο ομιλητής τον γράφει στο Agent.", otherCodes: "Άλλα δίκτυα και μεγάλος κωδικός",
    newCode: "Νέος κωδικός", sasLead: "Αριθμός επιβεβαίωσης", sasHint: "Ο ομιλητής βλέπει τον ίδιο αριθμό και πατάει Αποδοχή.",
    agents: "Agents", noAgents: "Κανένα Agent ακόμη.", noNetwork: "Χωρίς δίκτυο",
    take: "Ανάληψη ελέγχου", release: "Επιστροφή ελέγχου", tabPres: "Χειρισμός", tabFiles: "Αρχεία",
    prev: "◀ Προηγ.", next: "Επόμ. ▶", startShow: "Έναρξη προβολής", black: "Μαύρη οθόνη", unblack: "Επαναφορά εικόνας",
    goto: "Μετάβαση", endShow: "Τέλος προβολής", closeFile: "Κλείσιμο αρχείου",
    keysHint: "Πλήκτρα: ← → PageUp PageDown (και USB clicker), B μαύρη οθόνη, F5 έναρξη, Esc τέλος.",
    upload: "Αποστολή αρχείων", uploadDir: "Αποστολή φακέλου", mkdir: "Νέος φάκελος", refresh: "Ανανέωση",
    opFolder: "Φάκελος αυτόματης αποστολής", dropHere: "Σύρε εδώ αρχεία ή φακέλους για αποστολή",
    up: "Πάνω", openPres: "Άνοιγμα", download: "Λήψη", rename: "Μετονομασία", delete: "Διαγραφή", inUse: "σε χρήση",
    mkdirName: "Όνομα νέου φακέλου:", renameTo: "Νέο όνομα:", deleteConfirm: "Το «{0}» θα πάει στον Κάδο του ομιλητή. Συνέχεια;",
    hashOk: "ελέγχθηκε", online: "Συνδεδεμένο", offline: "Εκτός σύνδεσης", controlBy: "έλεγχος: {0}",
    takenBySupervisor: "Τον έλεγχο πήρε η εποπτεία ({0}).",
    language: "Γλώσσα", theme: "Θέμα", dark: "Σκούρο", light: "Ανοιχτό", updateCheck: "Ειδοποίηση για νέα σταθερή έκδοση",
    checkNow: "Έλεγχος τώρα", version: "Έκδοση", resetMode: "Αλλαγή ρόλου στην επόμενη εκκίνηση", resetDone: "Στην επόμενη εκκίνηση θα ρωτήσει ξανά.",
    save: "Αποθήκευση", cancel: "Άκυρο", ok: "OK", viewerHint: "Esc: τέλος προβολής",
    e_APP_NOT_FOUND: "Δεν υπάρχει εφαρμογή για αυτό το αρχείο στον υπολογιστή του ομιλητή. Δοκίμασε PDF στον ενσωματωμένο viewer.",
    e_PERMISSION_NEEDED: "Χρειάζεται άδεια στον υπολογιστή του ομιλητή.",
    e_UNSUPPORTED: "Αυτή η εφαρμογή δεν το κάνει. Άνοιξε το αρχείο με PowerPoint ή ως PDF.",
    e_HASH_MISMATCH: "Το αρχείο χάλασε στη μεταφορά. Στείλ' το ξανά.",
    e_NOT_HOLDER: "Πάτησε πρώτα «Ανάληψη ελέγχου».", e_NOT_ALLOWED: "Ο ρόλος σου δεν επιτρέπει αυτή την ενέργεια.",
    e_FILE_TYPE: "Ανοίγουν μόνο .pptx .ppt .key .odp .pdf.", e_BUSY: "Τον έλεγχο τον έχει άλλος.",
    e_IN_USE: "Το αρχείο παίζει τώρα. Κλείσ' το πρώτα.", e_NOT_FOUND: "Δεν βρέθηκε.", e_BAD_REQUEST: "Δεν έγινε:",
    e_STALE: "", e_LINK: "Η σύνδεση έπεσε, γίνεται επανασύνδεση.", e_IO: "Σφάλμα στον υπολογιστή του ομιλητή:",
  },
  en: {
    settings: "Settings", disconnectAll: "Disconnect all", updateGet: "Download", later: "Later",
    updateAvail: "New stable version {0} available.", upToDate: "You have the latest version.", noInternet: "No internet connection.",
    setupTitle: "What is this computer?", setupLead: "You choose once. Change it in Settings.",
    modeAgent: "Agent", modeAgentText: "The presenter's computer. Receives files and plays the presentation.",
    modeController: "Controller", modeControllerText: "The computer of the operator, the secretariat or the supervisor.",
    roleLead: "Role:", role_operator: "Operator", role_secretariat: "Secretariat", role_supervisor: "Supervisor",
    nameLabel: "Name others see", start: "Start",
    connect: "Connect", connectLead: "Type the code the Controller shows.", connectBtn: "Connect",
    connecting: "Connecting…", connectedOk: "Connected.", connected: "Connected", nobody: "Nobody yet.",
    disconnect: "Disconnect", hasControl: "has control", folder: "Files folder", openFolder: "Open folder",
    changeFolder: "Change folder", folderNext: "The new folder applies from the next start.",
    presentation: "Presentation", files: "Files", log: "Log", noFile: "No file open", emptyFolder: "The folder is empty.",
    statusConnected: "Connected ({0})", statusWaiting: "Waiting for a connection",
    st_idle: "Idle", st_open: "Ready", st_showing: "Showing", st_black: "Black screen",
    app_powerpoint: "PowerPoint", app_impress: "LibreOffice Impress", app_viewer: "Built-in PDF viewer", app_system: "System application",
    errCodeFormat: "The code has 12 or 16 characters.", errReach: "Controller not found. Same network? Try the long code or a cable.",
    errWrongCode: "Wrong code. Check the code on the Controller screen.", errRejected: "The connection was not accepted.",
    errVersion: "Agent and Controller need the same version. Update both.",
    promptTitle: "Connection request", promptWho: "{0} asks to connect as {1}.", promptHint: "The number must match the Controller screen.",
    promptRole: "Permissions", accept: "Accept", reject: "Reject",
    yourCode: "Your code", yourCodeLead: "The presenter types it on the Agent.", otherCodes: "Other networks and long code",
    newCode: "New code", sasLead: "Check number", sasHint: "The presenter sees the same number and presses Accept.",
    agents: "Agents", noAgents: "No Agent yet.", noNetwork: "No network",
    take: "Take control", release: "Release control", tabPres: "Control", tabFiles: "Files",
    prev: "◀ Prev", next: "Next ▶", startShow: "Start show", black: "Black screen", unblack: "Restore picture",
    goto: "Go to", endShow: "End show", closeFile: "Close file",
    keysHint: "Keys: ← → PageUp PageDown (and a USB clicker), B black screen, F5 start, Esc end.",
    upload: "Send files", uploadDir: "Send folder", mkdir: "New folder", refresh: "Refresh",
    opFolder: "Auto-send folder", dropHere: "Drop files or folders here to send them",
    up: "Up", openPres: "Open", download: "Download", rename: "Rename", delete: "Delete", inUse: "in use",
    mkdirName: "New folder name:", renameTo: "New name:", deleteConfirm: "“{0}” goes to the presenter's Recycle Bin. Continue?",
    hashOk: "verified", online: "Online", offline: "Offline", controlBy: "control: {0}",
    takenBySupervisor: "The supervisor ({0}) took control.",
    language: "Language", theme: "Theme", dark: "Dark", light: "Light", updateCheck: "Tell me about a new stable version",
    checkNow: "Check now", version: "Version", resetMode: "Change role at next start", resetDone: "It will ask again at the next start.",
    save: "Save", cancel: "Cancel", ok: "OK", viewerHint: "Esc: end show",
    e_APP_NOT_FOUND: "No application for this file on the presenter's computer. Try a PDF in the built-in viewer.",
    e_PERMISSION_NEEDED: "A permission is needed on the presenter's computer.",
    e_UNSUPPORTED: "This application cannot do that. Open the file with PowerPoint or as a PDF.",
    e_HASH_MISMATCH: "The file was damaged in transfer. Send it again.",
    e_NOT_HOLDER: "Press “Take control” first.", e_NOT_ALLOWED: "Your role cannot do this.",
    e_FILE_TYPE: "Only .pptx .ppt .key .odp .pdf open.", e_BUSY: "Someone else has control.",
    e_IN_USE: "This file is playing. Close it first.", e_NOT_FOUND: "Not found.", e_BAD_REQUEST: "Not done:",
    e_STALE: "", e_LINK: "The connection dropped, reconnecting.", e_IO: "Error on the presenter's computer:",
  },
};

let cur = "el";
export const lang = () => cur;
export function setLang(l) {
  cur = S[l] ? l : "el";
  document.documentElement.lang = cur;
}

export function T(key, ...args) {
  let s = S[cur][key] ?? S.el[key] ?? key;
  args.forEach((a, i) => (s = s.replace("{" + i + "}", a)));
  return s;
}

// errText turns an error code into a sentence and what to do.
export function errText(code, detail) {
  const k = "e_" + code;
  if (code === "STALE") return "";
  const base = S[cur][k] ?? S.el[k];
  if (!base) return detail || code;
  if (base.endsWith(":")) return base + " " + (detail || "");
  return base;
}

export function applyI18n() {
  document.querySelectorAll("[data-i18n]").forEach((el) => (el.textContent = T(el.dataset.i18n)));
  document.querySelectorAll("[data-i18n-title]").forEach((el) => {
    el.title = T(el.dataset.i18nTitle);
    el.setAttribute("aria-label", T(el.dataset.i18nTitle));
  });
}
