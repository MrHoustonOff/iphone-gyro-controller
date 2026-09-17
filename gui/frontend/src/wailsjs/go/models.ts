export namespace main {
	
	export class AppSettings {
	    theme: string;
	    lang: string;
	    activeSlot: number;
	    firstLaunchDone: boolean;
	    hideAuthor: boolean;
	    dsuPort: number;
	    httpPort: number;
	    httpsPort: number;
	    gyroDeadzone: number;
	    stillnessHint: boolean;
	    disconnectAlert: boolean;
	    soundMode: string;
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.lang = source["lang"];
	        this.activeSlot = source["activeSlot"];
	        this.firstLaunchDone = source["firstLaunchDone"];
	        this.hideAuthor = source["hideAuthor"];
	        this.dsuPort = source["dsuPort"];
	        this.httpPort = source["httpPort"];
	        this.httpsPort = source["httpsPort"];
	        this.gyroDeadzone = source["gyroDeadzone"];
	        this.stillnessHint = source["stillnessHint"];
	        this.disconnectAlert = source["disconnectAlert"];
	        this.soundMode = source["soundMode"];
	    }
	}
	export class Profile {
	    slot: number;
	    name: string;
	    device: string;
	    icon: string;
	    matrix: number[][];
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slot = source["slot"];
	        this.name = source["name"];
	        this.device = source["device"];
	        this.icon = source["icon"];
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
	    qx: number;
	    qy: number;
	    qz: number;
	    qw: number;
	    profiles: Profile[];
	    activeSlot: number;
	    activeMatrix: number[][];
	    ahrsQ0: number;
	    ahrsQ1: number;
	    ahrsQ2: number;
	    ahrsQ3: number;
	    firstLaunch: boolean;
	    hideAuthor: boolean;
	
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
	        this.qx = source["qx"];
	        this.qy = source["qy"];
	        this.qz = source["qz"];
	        this.qw = source["qw"];
	        this.profiles = this.convertValues(source["profiles"], Profile);
	        this.activeSlot = source["activeSlot"];
	        this.activeMatrix = source["activeMatrix"];
	        this.ahrsQ0 = source["ahrsQ0"];
	        this.ahrsQ1 = source["ahrsQ1"];
	        this.ahrsQ2 = source["ahrsQ2"];
	        this.ahrsQ3 = source["ahrsQ3"];
	        this.firstLaunch = source["firstLaunch"];
	        this.hideAuthor = source["hideAuthor"];
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
	    vector: number[];
	    errorCode: string;
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
	        this.vector = source["vector"];
	        this.errorCode = source["errorCode"];
	        this.errorMsg = source["errorMsg"];
	    }
	}
	
	export class ValidationResult {
	    success: boolean;
	    errorCode: string;
	    errorMsg: string;
	    matrix: number[][];
	    det: number;
	    pitchAxis: string;
	    yawAxis: string;
	    rollAxis: string;
	
	    static createFrom(source: any = {}) {
	        return new ValidationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.errorCode = source["errorCode"];
	        this.errorMsg = source["errorMsg"];
	        this.matrix = source["matrix"];
	        this.det = source["det"];
	        this.pitchAxis = source["pitchAxis"];
	        this.yawAxis = source["yawAxis"];
	        this.rollAxis = source["rollAxis"];
	    }
	}

}

