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
	
	    static createFrom(source: any = {}) {
	        return new Investigation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rootCause = source["rootCause"];
	        this.confidence = source["confidence"];
	        this.recommendedAction = source["recommendedAction"];
	        this.action = source["action"];
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
	    demo: boolean;
	    timeline: Event[];
	    investigation: Investigation;
	
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
	        this.demo = source["demo"];
	        this.timeline = this.convertValues(source["timeline"], Event);
	        this.investigation = this.convertValues(source["investigation"], Investigation);
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

