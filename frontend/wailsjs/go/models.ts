export namespace config {
	
	export class AIConfig {
	    enabled: boolean;
	    provider: string;
	    baseUrl: string;
	    model: string;
	    apiKey: string;
	
	    static createFrom(source: any = {}) {
	        return new AIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.provider = source["provider"];
	        this.baseUrl = source["baseUrl"];
	        this.model = source["model"];
	        this.apiKey = source["apiKey"];
	    }
	}
	export class UpdatesConfig {
	    autoCheckDisabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdatesConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.autoCheckDisabled = source["autoCheckDisabled"];
	    }
	}
	export class MonitoringConfig {
	    enabled: boolean;
	    intervalMinutes: number;
	    notify: boolean;
	    notifyMinSeverity: string;
	
	    static createFrom(source: any = {}) {
	        return new MonitoringConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.intervalMinutes = source["intervalMinutes"];
	        this.notify = source["notify"];
	        this.notifyMinSeverity = source["notifyMinSeverity"];
	    }
	}
	export class RemediationConfig {
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemediationConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	    }
	}
	export class Connection {
	    id: string;
	    type: string;
	    name: string;
	    enabled: boolean;
	    socket: string;
	    sshConnection: string;
	    remoteSocket: string;
	    remoteKubeconfig: string;
	    context: string;
	    kubeconfig: string;
	    url: string;
	    query: string;
	    token: string;
	    addr: string;
	    user: string;
	    keyPath: string;
	    knownHosts: string;
	    insecure: boolean;
	    expectedPorts: number[];
	
	    static createFrom(source: any = {}) {
	        return new Connection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.socket = source["socket"];
	        this.sshConnection = source["sshConnection"];
	        this.remoteSocket = source["remoteSocket"];
	        this.remoteKubeconfig = source["remoteKubeconfig"];
	        this.context = source["context"];
	        this.kubeconfig = source["kubeconfig"];
	        this.url = source["url"];
	        this.query = source["query"];
	        this.token = source["token"];
	        this.addr = source["addr"];
	        this.user = source["user"];
	        this.keyPath = source["keyPath"];
	        this.knownHosts = source["knownHosts"];
	        this.insecure = source["insecure"];
	        this.expectedPorts = source["expectedPorts"];
	    }
	}
	export class Config {
	    environment: string;
	    windowMinutes: number;
	    ignoreSubjects: string[];
	    connections: Connection[];
	    ai: AIConfig;
	    remediation: RemediationConfig;
	    monitoring: MonitoringConfig;
	    updates: UpdatesConfig;
	    webhooks: notify.Channel[];
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.environment = source["environment"];
	        this.windowMinutes = source["windowMinutes"];
	        this.ignoreSubjects = source["ignoreSubjects"];
	        this.connections = this.convertValues(source["connections"], Connection);
	        this.ai = this.convertValues(source["ai"], AIConfig);
	        this.remediation = this.convertValues(source["remediation"], RemediationConfig);
	        this.monitoring = this.convertValues(source["monitoring"], MonitoringConfig);
	        this.updates = this.convertValues(source["updates"], UpdatesConfig);
	        this.webhooks = this.convertValues(source["webhooks"], notify.Channel);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	

}

export namespace engine {
	
	export class Result {
	    incidents: model.Incident[];
	    collectors: string[];
	    errors: Record<string, string>;
	    warnings: string[];
	    // Go type: time
	    scannedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.incidents = this.convertValues(source["incidents"], model.Incident);
	        this.collectors = source["collectors"];
	        this.errors = source["errors"];
	        this.warnings = source["warnings"];
	        this.scannedAt = this.convertValues(source["scannedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace history {
	
	export class MuteRule {
	    key: string;
	    title: string;
	    environment: string;
	    by: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new MuteRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.title = source["title"];
	        this.environment = source["environment"];
	        this.by = source["by"];
	        this.at = this.convertValues(source["at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Record {
	    id: string;
	    key: string;
	    title: string;
	    severity: string;
	    status: string;
	    // Go type: time
	    firstSeen: any;
	    // Go type: time
	    lastSeen: any;
	    // Go type: time
	    resolvedAt?: any;
	    scans: number;
	    reopened: number;
	    latest: model.Incident;
	    ackBy?: string;
	    // Go type: time
	    ackAt?: any;
	    // Go type: time
	    snoozedUntil?: any;
	    muted?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Record(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.key = source["key"];
	        this.title = source["title"];
	        this.severity = source["severity"];
	        this.status = source["status"];
	        this.firstSeen = this.convertValues(source["firstSeen"], null);
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	        this.resolvedAt = this.convertValues(source["resolvedAt"], null);
	        this.scans = source["scans"];
	        this.reopened = source["reopened"];
	        this.latest = this.convertValues(source["latest"], model.Incident);
	        this.ackBy = source["ackBy"];
	        this.ackAt = this.convertValues(source["ackAt"], null);
	        this.snoozedUntil = this.convertValues(source["snoozedUntil"], null);
	        this.muted = source["muted"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class AITestResult {
	    ok: boolean;
	    message: string;
	    models: string[];
	    remote: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AITestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.models = source["models"];
	        this.remote = source["remote"];
	    }
	}
	export class WebhookStatus {
	    id: string;
	    // Go type: time
	    at: any;
	    ok: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new WebhookStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.at = this.convertValues(source["at"], null);
	        this.ok = source["ok"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MonitorStatus {
	    enabled: boolean;
	    intervalMinutes: number;
	    // Go type: time
	    lastScan: any;
	    // Go type: time
	    nextScan: any;
	    scanning: boolean;
	    notify: boolean;
	    notificationsError: string;
	    historyError: string;
	    webhooks: WebhookStatus[];
	
	    static createFrom(source: any = {}) {
	        return new MonitorStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.intervalMinutes = source["intervalMinutes"];
	        this.lastScan = this.convertValues(source["lastScan"], null);
	        this.nextScan = this.convertValues(source["nextScan"], null);
	        this.scanning = source["scanning"];
	        this.notify = source["notify"];
	        this.notificationsError = source["notificationsError"];
	        this.historyError = source["historyError"];
	        this.webhooks = this.convertValues(source["webhooks"], WebhookStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RollbackPreview {
	    token: string;
	    incidentId: string;
	    plan: remediate.Plan;
	    confirmText: string;
	    expiresIn: number;
	
	    static createFrom(source: any = {}) {
	        return new RollbackPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.incidentId = source["incidentId"];
	        this.plan = this.convertValues(source["plan"], remediate.Plan);
	        this.confirmText = source["confirmText"];
	        this.expiresIn = source["expiresIn"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Settings {
	    config: config.Config;
	    path: string;
	    saved: boolean;
	    notes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], config.Config);
	        this.path = source["path"];
	        this.saved = source["saved"];
	        this.notes = source["notes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TestResult {
	    ok: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	    }
	}

}

export namespace model {
	
	export class Event {
	    // Go type: time
	    time: any;
	    source: string;
	    kind: string;
	    severity: string;
	    title: string;
	    detail: string;
	    subject: string;
	    origin: string;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = this.convertValues(source["time"], null);
	        this.source = source["source"];
	        this.kind = source["kind"];
	        this.severity = source["severity"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.subject = source["subject"];
	        this.origin = source["origin"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Investigation {
	    rootCause: string;
	    confidence: number;
	    recommendedAction: string;
	    action: string;
	    target: string;
	    targetSource: string;
	    targetConnection: string;
	    reasoning: string[];
	    source: string;
	    model: string;
	    aiStatus: string;
	    aiNote: string;
	    citedEvents: number[];
	    ruleBasedRootCause: string;
	
	    static createFrom(source: any = {}) {
	        return new Investigation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootCause = source["rootCause"];
	        this.confidence = source["confidence"];
	        this.recommendedAction = source["recommendedAction"];
	        this.action = source["action"];
	        this.target = source["target"];
	        this.targetSource = source["targetSource"];
	        this.targetConnection = source["targetConnection"];
	        this.reasoning = source["reasoning"];
	        this.source = source["source"];
	        this.model = source["model"];
	        this.aiStatus = source["aiStatus"];
	        this.aiNote = source["aiNote"];
	        this.citedEvents = source["citedEvents"];
	        this.ruleBasedRootCause = source["ruleBasedRootCause"];
	    }
	}
	export class Incident {
	    id: string;
	    title: string;
	    severity: string;
	    environment: string;
	    // Go type: time
	    detectedAt: any;
	    automatic: boolean;
	    timeline: Event[];
	    investigation: Investigation;
	    ackBy?: string;
	    // Go type: time
	    ackAt?: any;
	    // Go type: time
	    snoozedUntil?: any;
	    muted?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Incident(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.severity = source["severity"];
	        this.environment = source["environment"];
	        this.detectedAt = this.convertValues(source["detectedAt"], null);
	        this.automatic = source["automatic"];
	        this.timeline = this.convertValues(source["timeline"], Event);
	        this.investigation = this.convertValues(source["investigation"], Investigation);
	        this.ackBy = source["ackBy"];
	        this.ackAt = this.convertValues(source["ackAt"], null);
	        this.snoozedUntil = this.convertValues(source["snoozedUntil"], null);
	        this.muted = source["muted"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace notify {
	
	export class Channel {
	    id: string;
	    type: string;
	    name: string;
	    enabled: boolean;
	    url: string;
	    minSeverity: string;
	
	    static createFrom(source: any = {}) {
	        return new Channel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.url = source["url"];
	        this.minSeverity = source["minSeverity"];
	    }
	}

}

export namespace remediate {
	
	export class Entry {
	    // Go type: time
	    time: any;
	    user: string;
	    phase: string;
	    incidentId: string;
	    action: string;
	    platform: string;
	    connection: string;
	    target: string;
	    command: string;
	    ok: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = this.convertValues(source["time"], null);
	        this.user = source["user"];
	        this.phase = source["phase"];
	        this.incidentId = source["incidentId"];
	        this.action = source["action"];
	        this.platform = source["platform"];
	        this.connection = source["connection"];
	        this.target = source["target"];
	        this.command = source["command"];
	        this.ok = source["ok"];
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Plan {
	    platform: string;
	    connection: string;
	    target: string;
	    feasible: boolean;
	    reason: string;
	    current: string;
	    previous: string;
	    command: string;
	    notes: string[];
	    changes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Plan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.platform = source["platform"];
	        this.connection = source["connection"];
	        this.target = source["target"];
	        this.feasible = source["feasible"];
	        this.reason = source["reason"];
	        this.current = source["current"];
	        this.previous = source["previous"];
	        this.command = source["command"];
	        this.notes = source["notes"];
	        this.changes = source["changes"];
	    }
	}
	export class Result {
	    applied: boolean;
	    healthy: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.applied = source["applied"];
	        this.healthy = source["healthy"];
	        this.message = source["message"];
	    }
	}

}

export namespace ssh {
	
	export class HostKeyInfo {
	    addr: string;
	    keyType: string;
	    fingerprint: string;
	    status: string;
	    knownHostsPath: string;
	    known: string[];
	
	    static createFrom(source: any = {}) {
	        return new HostKeyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.keyType = source["keyType"];
	        this.fingerprint = source["fingerprint"];
	        this.status = source["status"];
	        this.knownHostsPath = source["knownHostsPath"];
	        this.known = source["known"];
	    }
	}

}

export namespace updater {
	
	export class Asset {
	    name: string;
	    type: string;
	    size: number;
	    isDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Asset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.size = source["size"];
	        this.isDefault = source["isDefault"];
	    }
	}
	export class Info {
	    available: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    releaseName: string;
	    releaseNotes: string;
	    publishedAt: string;
	    releaseUrl: string;
	    platform: string;
	    installType: string;
	    needsElevation: boolean;
	    canInstall: boolean;
	    reason: string;
	    assets: Asset[];
	    noReleases: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.releaseName = source["releaseName"];
	        this.releaseNotes = source["releaseNotes"];
	        this.publishedAt = source["publishedAt"];
	        this.releaseUrl = source["releaseUrl"];
	        this.platform = source["platform"];
	        this.installType = source["installType"];
	        this.needsElevation = source["needsElevation"];
	        this.canInstall = source["canInstall"];
	        this.reason = source["reason"];
	        this.assets = this.convertValues(source["assets"], Asset);
	        this.noReleases = source["noReleases"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Progress {
	    status: string;
	    percentage: number;
	    readBytes: number;
	    totalBytes: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Progress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.percentage = source["percentage"];
	        this.readBytes = source["readBytes"];
	        this.totalBytes = source["totalBytes"];
	        this.message = source["message"];
	    }
	}

}

