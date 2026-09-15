export namespace main {
	
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
	    }
	}

}

