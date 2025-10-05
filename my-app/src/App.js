import { useState, useEffect } from 'react';


function MyComponent() {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
    
  useEffect(() => {
      const fetchData = async () => {
          try {
              const response = await fetch('http://localhost:8080/api/users'); // Replace with your API endpoint
              if (!response.ok) {
                  throw new Error(`HTTP error! status: ${response.status}`);
              }
              setData(await response.text());
          } catch (error) {
              setError(error);
          } finally {
              setLoading(false);
          }
      };
      
      fetchData();
  }, []); // Empty dependency array ensures this runs only once on mount

  if (loading) return <p>Loading data...</p>;
  if (error) return <p>Error: {error.message}</p>;
    
  return (
    <div>
      <h1>Fetched Data:</h1>
      {/* Render your data here */}
          <pre>{data}</pre>
    </div>
  );
}

function MyButton({count, onClick}) {

  return (
    <button onClick={onClick}>
          Clicked {count} times
    </button>
  );
}

export default function MyApp() {
  const [count, setCount] = useState(0);
    
  function handleClick() {
      setCount(count + 1)
  }

    
  return (
    <div>
      <h1>Welcome to my app</h1>
      <MyButton count={count} onClick={handleClick} />      
          <MyButton count={count} onClick={handleClick} />
          <MyComponent />
    </div>
  );
}
