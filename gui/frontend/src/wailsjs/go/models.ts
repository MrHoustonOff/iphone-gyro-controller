export namespace main {
	
	export class Profile {
	    slot: number;
	    name: string;
	    matrix: number[][];
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slot = source["slot"];
	        this.name = source["name"];
	        this.matrix = source["matrix"];
	        this.active = source["active"];
	    }
	}
	export class AppState {
	    status: string;
	    isPaused: boolean;
	    deviceName: string;
	    hz: number;
	    pingMs: number;
	    connectedTime: string;
	    pitch: number;
	    roll: number;
	    yaw: number;
	    ip: string;
	    gamepadUrl: string;
	    setupUrl: string;
	    qrCode: string;
	    setupQrCode: string;
	    rawRotX: number;
	    rawRotY: number;
	    rawRotZ: number;
	    rawAccX: number;
	    rawAccY: number;
	    rawAccZ: number;
	    profiles: Profile[];
	    activeSlot: number;
	
	    static createFrom(source: any = {}) {
	        return new AppState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.isPaused = source["isPaused"];
	        this.deviceName = source["deviceName"];
	        this.hz = source["hz"];
	        this.pingMs = source["pingMs"];
	        this.connectedTime = source["connectedTime"];
	        this.pitch = source["pitch"];
	        this.roll = source["roll"];
	        this.yaw = source["yaw"];
	        this.ip = source["ip"];
	        this.gamepadUrl = source["gamepadUrl"];
	        this.setupUrl = source["setupUrl"];
	        this.qrCode = source["qrCode"];
	        this.setupQrCode = source["setupQrCode"];
	        this.rawRotX = source["rawRotX"];
	        this.rawRotY = source["rawRotY"];
	        this.rawRotZ = source["rawRotZ"];
	        this.rawAccX = source["rawAccX"];
	        this.rawAccY = source["rawAccY"];
	        this.rawAccZ = source["rawAccZ"];
	        this.profiles = this.convertValues(source["profiles"], Profile);
	        this.activeSlot = source["activeSlot"];
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
	export class CaptureResult {
	    success: boolean;
	    axisIdx: number;
	    sign: number;
	    axisName: string;
	    confidence: number;
	    sampleCount: number;
	    peakSpeed: number;
	    errorMsg: string;
	
	    static createFrom(source: any = {}) {
	        return new CaptureResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.axisIdx = source["axisIdx"];
	        this.sign = source["sign"];
	        this.axisName = source["axisName"];
	        this.confidence = source["confidence"];
	        this.sampleCount = source["sampleCount"];
	        this.peakSpeed = source["peakSpeed"];
	        this.errorMsg = source["errorMsg"];
	    }
	}

}

