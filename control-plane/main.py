from fastapi import FastAPI, HTTPException
from models.config import GatewayConfig, RouteConfig
import json
import os

app = FastAPI(title="API Gateway Control Plane")

CONFIG_FILE = "../config/gateway.json"

@app.get("/")
async def root():
    return {"message": "API Gateway Control Plane"}

@app.get("/api/config")
async def get_config():
    """Get current gateway configuration"""
    try:
        with open(CONFIG_FILE, 'r') as f:
            return json.load(f)
    except FileNotFoundError:
        raise HTTPException(status_code=404, detail="Config file not found")

@app.post("/api/config")
async def update_config(config: GatewayConfig):
    """Update gateway configuration"""
    try:
        with open(CONFIG_FILE, 'w') as f:
            json.dump(config.dict(), f, indent=2)
        return {"message": "Configuration updated", "config": config.dict()}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/routes")
async def add_route(route: RouteConfig):
    """Add or update a route"""
    try:
        with open(CONFIG_FILE, 'r') as f:
            config_data = json.load(f)
        
        # Update or add route
        routes = config_data.get('routes', [])
        found = False
        for i, r in enumerate(routes):
            if r['path'] == route.path:
                routes[i] = route.dict()
                found = True
                break
        
        if not found:
            routes.append(route.dict())
        
        config_data['routes'] = routes
        
        with open(CONFIG_FILE, 'w') as f:
            json.dump(config_data, f, indent=2)
        
        return {"message": "Route updated", "route": route.dict()}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
